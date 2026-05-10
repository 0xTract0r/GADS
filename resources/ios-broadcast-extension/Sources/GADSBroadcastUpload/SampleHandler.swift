import AVFoundation
import CoreMedia
import CoreVideo
import Foundation
import Network
import ReplayKit
import VideoToolbox

final class SampleHandler: RPBroadcastSampleHandler {
    private let queue = DispatchQueue(label: "com.gads.broadcast.upload")
    private var listener: NWListener?
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    private var compressionSession: VTCompressionSession?
    private var encodedWidth: Int = 0
    private var encodedHeight: Int = 0
    private var nalLengthFieldBytes: Int = 4
    private var needsKeyframe = true
    private var lastTimestampMicros: UInt64 = 0
    private var latestImageBuffer: CVPixelBuffer?
    private var repeatTimer: DispatchSourceTimer?
    private let targetFrameIntervalMicros: UInt64 = 1_000_000 / 24
    private var lastSubmittedWallClockMicros: UInt64 = 0
    private var lastSubmittedPresentationMicros: UInt64 = 0

    override func broadcastStarted(withSetupInfo setupInfo: [String: NSObject]?) {
        do {
            try startTCPServer()
        } catch {
            finishBroadcastWithError(error)
        }
    }

    override func broadcastPaused() {
        queue.async { [weak self] in
            self?.needsKeyframe = true
        }
    }

    override func broadcastResumed() {
        queue.async { [weak self] in
            self?.needsKeyframe = true
        }
    }

    override func broadcastFinished() {
        queue.sync {
            teardown()
        }
    }

    override func processSampleBuffer(_ sampleBuffer: CMSampleBuffer, with sampleBufferType: RPSampleBufferType) {
        guard sampleBufferType == .video,
              let imageBuffer = CMSampleBufferGetImageBuffer(sampleBuffer)
        else {
            return
        }

        queue.async { [weak self] in
            self?.storeLatestImageBuffer(imageBuffer)
        }
    }

    private func startTCPServer() throws {
        let port = NWEndpoint.Port(rawValue: 8765)!
        let listener = try NWListener(using: .tcp, on: port)
        listener.newConnectionHandler = { [weak self] newConnection in
            guard let self else {
                newConnection.cancel()
                return
            }
            self.queue.async {
                self.connections[ObjectIdentifier(newConnection)] = newConnection
                self.needsKeyframe = true
                self.startFrameRepeaterIfNeeded()
                newConnection.stateUpdateHandler = { [weak self, weak newConnection] state in
                    guard let self, let newConnection else { return }
                    if case .failed = state {
                        self.clearConnection(newConnection)
                    }
                    if case .cancelled = state {
                        self.clearConnection(newConnection)
                    }
                }
                newConnection.start(queue: self.queue)
                self.submitLatestFrame(forceKeyframe: true)
            }
        }
        listener.start(queue: queue)
        self.listener = listener
    }

    private func clearConnection(_ candidate: NWConnection) {
        queue.async { [weak self, weak candidate] in
            guard let self, let candidate else { return }
            self.connections.removeValue(forKey: ObjectIdentifier(candidate))
            self.stopFrameRepeaterIfIdle()
        }
    }

    private func teardown() {
        for connection in connections.values {
            connection.cancel()
        }
        connections.removeAll()
        listener?.cancel()
        listener = nil
        repeatTimer?.cancel()
        repeatTimer = nil
        latestImageBuffer = nil
        if let compressionSession {
            VTCompressionSessionCompleteFrames(compressionSession, untilPresentationTimeStamp: .invalid)
            VTCompressionSessionInvalidate(compressionSession)
            self.compressionSession = nil
        }
    }

    private func encode(imageBuffer: CVImageBuffer, presentationTime: CMTime) {
        guard ensureCompressionSession(for: imageBuffer) else { return }
        guard let compressionSession else { return }

        let frameProperties: CFDictionary?
        if needsKeyframe {
            frameProperties = [kVTEncodeFrameOptionKey_ForceKeyFrame: true] as CFDictionary
            needsKeyframe = false
        } else {
            frameProperties = nil
        }

        let encodeStatus = VTCompressionSessionEncodeFrame(
            compressionSession,
            imageBuffer: imageBuffer,
            presentationTimeStamp: presentationTime,
            duration: .invalid,
            frameProperties: frameProperties,
            sourceFrameRefcon: nil,
            infoFlagsOut: nil
        )
        if encodeStatus == noErr {
            lastSubmittedWallClockMicros = nowMicros()
        } else {
            needsKeyframe = true
        }
    }

    private func storeLatestImageBuffer(_ imageBuffer: CVPixelBuffer) {
        latestImageBuffer = imageBuffer
        if !connections.isEmpty {
            submitLatestFrame(forceKeyframe: false)
        }
    }

    private func startFrameRepeaterIfNeeded() {
        guard repeatTimer == nil else { return }

        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(
            deadline: .now() + .milliseconds(Int(targetFrameIntervalMicros / 1_000)),
            repeating: .microseconds(Int(targetFrameIntervalMicros)),
            leeway: .milliseconds(5)
        )
        timer.setEventHandler { [weak self] in
            self?.repeatLatestFrameIfNeeded()
        }
        timer.resume()
        repeatTimer = timer
    }

    private func stopFrameRepeaterIfIdle() {
        guard connections.isEmpty else { return }
        repeatTimer?.cancel()
        repeatTimer = nil
        lastSubmittedWallClockMicros = 0
        needsKeyframe = true
    }

    private func repeatLatestFrameIfNeeded() {
        guard !connections.isEmpty else {
            stopFrameRepeaterIfIdle()
            return
        }

        let now = nowMicros()
        if lastSubmittedWallClockMicros != 0,
           now - lastSubmittedWallClockMicros < targetFrameIntervalMicros {
            return
        }

        submitLatestFrame(forceKeyframe: false)
    }

    private func submitLatestFrame(forceKeyframe: Bool) {
        guard let latestImageBuffer else { return }
        if forceKeyframe {
            needsKeyframe = true
        }
        encode(imageBuffer: latestImageBuffer, presentationTime: nextPresentationTime())
    }

    private func nextPresentationTime() -> CMTime {
        var candidate = nowMicros()
        if candidate <= lastSubmittedPresentationMicros {
            candidate = lastSubmittedPresentationMicros + targetFrameIntervalMicros
        }
        lastSubmittedPresentationMicros = candidate
        return CMTime(value: CMTimeValue(candidate), timescale: 1_000_000)
    }

    private func nowMicros() -> UInt64 {
        DispatchTime.now().uptimeNanoseconds / 1_000
    }

    private func ensureCompressionSession(for imageBuffer: CVImageBuffer) -> Bool {
        let width = CVPixelBufferGetWidth(imageBuffer)
        let height = CVPixelBufferGetHeight(imageBuffer)
        if compressionSession != nil, width == encodedWidth, height == encodedHeight {
            return true
        }

        if let compressionSession {
            VTCompressionSessionInvalidate(compressionSession)
            self.compressionSession = nil
        }

        let callback: VTCompressionOutputCallback = { refCon, _, status, _, sampleBuffer in
            guard status == noErr,
                  let refCon,
                  let sampleBuffer
            else {
                return
            }
            let handler = Unmanaged<SampleHandler>.fromOpaque(refCon).takeUnretainedValue()
            handler.queue.async {
                handler.handleEncodedSampleBuffer(sampleBuffer)
            }
        }

        var session: VTCompressionSession?
        let createStatus = VTCompressionSessionCreate(
            allocator: kCFAllocatorDefault,
            width: Int32(width),
            height: Int32(height),
            codecType: kCMVideoCodecType_H264,
            encoderSpecification: nil,
            imageBufferAttributes: nil,
            compressedDataAllocator: nil,
            outputCallback: callback,
            refcon: Unmanaged.passUnretained(self).toOpaque(),
            compressionSessionOut: &session
        )
        guard createStatus == noErr, let session else {
            return false
        }

        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_RealTime, value: kCFBooleanTrue)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AllowFrameReordering, value: kCFBooleanFalse)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ProfileLevel, value: kVTProfileLevel_H264_Baseline_AutoLevel)
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_MaxKeyFrameInterval, value: NSNumber(value: 24))
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_ExpectedFrameRate, value: NSNumber(value: 24))
        VTSessionSetProperty(session, key: kVTCompressionPropertyKey_AverageBitRate, value: NSNumber(value: 2_000_000))
        VTCompressionSessionPrepareToEncodeFrames(session)

        compressionSession = session
        encodedWidth = width
        encodedHeight = height
        needsKeyframe = true
        return true
    }

    private func handleEncodedSampleBuffer(_ sampleBuffer: CMSampleBuffer) {
        guard !connections.isEmpty,
              let payload = makeAnnexBPayload(from: sampleBuffer),
              !payload.isEmpty
        else {
            return
        }

        let timestamp = timestampMicros(for: sampleBuffer)
        sendFrame(payload, timestampMicros: timestamp)
    }

    private func makeAnnexBPayload(from sampleBuffer: CMSampleBuffer) -> Data? {
        guard let blockBuffer = CMSampleBufferGetDataBuffer(sampleBuffer) else {
            return nil
        }

        var payload = Data()
        let startCode = Data([0x00, 0x00, 0x00, 0x01])
        if isKeyframe(sampleBuffer), let formatDescription = CMSampleBufferGetFormatDescription(sampleBuffer) {
            appendParameterSet(index: 0, from: formatDescription, to: &payload, startCode: startCode)
            appendParameterSet(index: 1, from: formatDescription, to: &payload, startCode: startCode)
        }

        let totalLength = CMBlockBufferGetDataLength(blockBuffer)
        guard totalLength > nalLengthFieldBytes else {
            return payload
        }

        var bytes = [UInt8](repeating: 0, count: totalLength)
        let copyStatus = CMBlockBufferCopyDataBytes(
            blockBuffer,
            atOffset: 0,
            dataLength: totalLength,
            destination: &bytes
        )
        guard copyStatus == noErr else {
            return nil
        }

        var offset = 0
        while offset + nalLengthFieldBytes <= totalLength {
            var nalLength = 0
            for index in 0..<nalLengthFieldBytes {
                nalLength = (nalLength << 8) | Int(bytes[offset + index])
            }
            offset += nalLengthFieldBytes

            guard nalLength > 0, offset + nalLength <= totalLength else {
                break
            }

            payload.append(startCode)
            payload.append(contentsOf: bytes[offset..<(offset + nalLength)])
            offset += nalLength
        }

        return payload
    }

    private func appendParameterSet(
        index: Int,
        from formatDescription: CMFormatDescription,
        to payload: inout Data,
        startCode: Data
    ) {
        var parameterSetPointer: UnsafePointer<UInt8>?
        var parameterSetSize = 0
        var parameterSetCount = 0
        var nalUnitHeaderLength: Int32 = 0
        let status = CMVideoFormatDescriptionGetH264ParameterSetAtIndex(
            formatDescription,
            parameterSetIndex: index,
            parameterSetPointerOut: &parameterSetPointer,
            parameterSetSizeOut: &parameterSetSize,
            parameterSetCountOut: &parameterSetCount,
            nalUnitHeaderLengthOut: &nalUnitHeaderLength
        )
        guard status == noErr,
              let parameterSetPointer,
              parameterSetSize > 0
        else {
            return
        }

        nalLengthFieldBytes = max(1, Int(nalUnitHeaderLength))
        payload.append(startCode)
        payload.append(
            contentsOf: UnsafeBufferPointer(
                start: parameterSetPointer,
                count: parameterSetSize
            )
        )
    }

    private func isKeyframe(_ sampleBuffer: CMSampleBuffer) -> Bool {
        guard let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[CFString: Any]],
              let firstAttachment = attachments.first,
              let notSync = firstAttachment[kCMSampleAttachmentKey_NotSync] as? Bool
        else {
            return true
        }
        return !notSync
    }

    private func timestampMicros(for sampleBuffer: CMSampleBuffer) -> UInt64 {
        let presentationTime = CMSampleBufferGetPresentationTimeStamp(sampleBuffer)
        let seconds = CMTimeGetSeconds(presentationTime)
        let candidate: UInt64
        if seconds.isFinite, seconds > 0 {
            candidate = UInt64(seconds * 1_000_000)
        } else {
            candidate = DispatchTime.now().uptimeNanoseconds / 1_000
        }

        if candidate <= lastTimestampMicros {
            lastTimestampMicros += 1
            return lastTimestampMicros
        }

        lastTimestampMicros = candidate
        return candidate
    }

    private func sendFrame(_ payload: Data, timestampMicros: UInt64) {
        sendPacket(payload, timestampMicros: timestampMicros)
    }

    private func sendPacket(_ payload: Data, timestampMicros: UInt64) {
        guard !connections.isEmpty else { return }

        var length = UInt32(payload.count).bigEndian
        var timestamp = timestampMicros.bigEndian
        var packet = Data(bytes: &length, count: MemoryLayout<UInt32>.size)
        packet.append(Data(bytes: &timestamp, count: MemoryLayout<UInt64>.size))
        packet.append(payload)

        for connection in connections.values {
            connection.send(content: packet, completion: .contentProcessed { [weak self, weak connection] error in
                guard error != nil, let self, let connection else { return }
                self.clearConnection(connection)
            })
        }
    }
}
