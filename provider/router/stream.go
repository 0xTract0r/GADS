/*
 * This file is part of GADS.
 *
 * Copyright (c) 2022-2025 Nikola Shabanov
 *
 * This source code is licensed under the GNU Affero General Public License v3.0.
 * You may obtain a copy of the license at https://www.gnu.org/licenses/agpl-3.0.html
 */

package router

import (
	"GADS/common/models"
	"GADS/provider/config"
	"GADS/provider/logger"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"GADS/provider/devices"

	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	iosMJPEGCopyBufferSize        = 64 * 1024
	iosMJPEGMaxFrameBytes   int64 = 32 * 1024 * 1024
	iosMJPEGHeaderTimeout         = 5 * time.Second
	iosMJPEGReadIdleTimeout       = 15 * time.Second
)

var errIOSMJPEGFrameTooLarge = errors.New("iOS MJPEG frame exceeds safety limit")

type iosMJPEGProxyOptions struct {
	copyBufferSize int
	maxFrameBytes  int64
}

var defaultIOSMJPEGProxyOptions = iosMJPEGProxyOptions{
	copyBufferSize: iosMJPEGCopyBufferSize,
	maxFrameBytes:  iosMJPEGMaxFrameBytes,
}

type idleReadConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleReadConn) Read(buffer []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(buffer)
}

func newIOSMJPEGHTTPClient(headerTimeout, readIdleTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{
		Timeout:   headerTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &idleReadConn{Conn: conn, timeout: readIdleTimeout}, nil
	}
	transport.ResponseHeaderTimeout = headerTimeout
	transport.DisableKeepAlives = true
	return &http.Client{Transport: transport}
}

var iosMJPEGHTTPClient = newIOSMJPEGHTTPClient(iosMJPEGHeaderTimeout, iosMJPEGReadIdleTimeout)

func IOSStreamMJPEGAuto(c *gin.Context) {
	udid := c.Param("udid")
	platDev, deviceFound := devices.DevManager.Get(udid)
	if !deviceFound {
		logger.ProviderLogger.LogError("IOSStreamMJPEGAuto", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	if iosDev, ok := platDev.(*devices.IOSDevice); ok && iosDev.GetDBDevice().StreamType == models.MJPEGStreamTypeId {
		IOSStreamMJPEGWda(c)
		return
	}

	if config.ProviderConfig.UseGadsIosStream {
		IOSStreamMJPEG(c)
		return
	}
	IOSStreamMJPEGWda(c)
}

func AndroidStreamProxy(c *gin.Context) {
	udid := c.Param("udid")
	platDev, deviceFound := devices.DevManager.Get(udid)
	if !deviceFound {
		logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	rcDev, isRcDevice := platDev.(devices.RemoteControllable)
	if !isRcDevice {
		logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Device `%s` does not support streaming", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed upgrading http to ws for device `%s` - %s", udid, err))
		return
	}
	defer conn.Close()

	u := url.URL{Scheme: "ws", Host: "localhost:" + rcDev.GetStreamPort(), Path: ""}
	destConn, _, _, err := ws.DefaultDialer.Dial(context.Background(), u.String())
	if err != nil {
		logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed connecting to device `%s` stream port - %s", udid, err))
		return
	}
	defer destConn.Close()

	// Read messages(jpegs) from the device streaming websocket server
	// And send them to the provider websocket client
	for {
		data, code, err := wsutil.ReadServerData(destConn)
		if err != nil {
			logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed reading data from device `%s` ws conn - %s", udid, err))
			return
		}

		err = wsutil.WriteServerMessage(conn, code, data)
		if err != nil {
			logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed writing data to provider ws connection for device `%s` - %s", udid, err))
			return
		}
	}
}

func AndroidStreamMJPEG(c *gin.Context) {
	c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	c.Writer.WriteHeader(http.StatusOK)
	c.Deadline()

	udid := c.Param("udid")
	platDev, deviceFound := devices.DevManager.Get(udid)
	if !deviceFound {
		logger.ProviderLogger.LogError("AndroidStreamMJPEG", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	rcDev, isRcDevice := platDev.(devices.RemoteControllable)
	if !isRcDevice {
		logger.ProviderLogger.LogError("AndroidStreamMJPEG", fmt.Sprintf("Device `%s` does not support streaming", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	u := url.URL{Scheme: "ws", Host: "localhost:" + rcDev.GetStreamPort(), Path: ""}
	conn, _, _, err := ws.DefaultDialer.Dial(context.Background(), u.String())
	if err != nil {
		logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed connecting to device `%s` stream port - %s", udid, err))
		return
	}
	defer conn.Close()

	// Read messages(jpegs) from the device streaming websocket server
	// And send them to the provider websocket client
	for {
		data, _, err := wsutil.ReadServerData(conn)
		if err != nil {
			logger.ProviderLogger.LogError("AndroidStreamProxy", fmt.Sprintf("Failed reading data from device `%s` ws conn - %s", udid, err))
			return
		}

		// Write the boundary and content type for each frame
		_, err = c.Writer.Write([]byte("\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n"))
		if err != nil {
			break
		}

		// Write the image to the response
		_, err = c.Writer.Write(data)
		if err != nil {
			break
		}

		// Flush the response writer to ensure the client receives the frame immediately
		c.Writer.Flush()
	}
}

func findJPEGMarkers(data []byte) (int, int) {
	start := bytes.Index(data, []byte{0xFF, 0xD8})
	end := bytes.Index(data, []byte{0xFF, 0xD9})
	return start, end
}

func IOSStreamMJPEG(c *gin.Context) {
	// Set the necessary headers for MJPEG streaming
	// Note: The "boundary" is arbitrary but must be unique and consistent.
	c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	c.Writer.WriteHeader(http.StatusOK)
	c.Deadline()

	udid := c.Param("udid")
	platDev, deviceFound := devices.DevManager.Get(udid)
	if !deviceFound {
		logger.ProviderLogger.LogError("IOSStreamMJPEG", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	rcDev, isRcDevice := platDev.(devices.RemoteControllable)
	if !isRcDevice {
		logger.ProviderLogger.LogError("IOSStreamMJPEG", fmt.Sprintf("Device `%s` does not support streaming", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// Read data from device
	server := "localhost:" + rcDev.GetStreamPort()
	// Connect to the server
	conn, err := net.Dial("tcp", server)
	if err != nil {
		os.Exit(1)
	}
	defer conn.Close()

	var buffer []byte
	for {

		// Read data from the connection
		tempBuf := make([]byte, 1024)
		n, err := conn.Read(tempBuf)
		if err != nil {
			if err != io.EOF {
				return
			}
			break
		}

		// Append the read bytes to the buffer
		buffer = append(buffer, tempBuf[:n]...)

		// Check if buffer has a complete JPEG image
		start, end := findJPEGMarkers(buffer)
		if start >= 0 && end > start {
			// Process the JPEG image
			jpegImage := buffer[start : end+2] // Include end marker
			// Keep any remaining data in the buffer for the next image
			buffer = buffer[end+2:]

			// Write the boundary and content type for each frame
			_, err = c.Writer.Write([]byte("\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n"))
			if err != nil {
				break
			}

			// Write the image to the response
			_, err = c.Writer.Write(jpegImage)
			if err != nil {
				break
			}

			// Flush the response writer to ensure the client receives the frame immediately
			c.Writer.Flush()
		}
	}
}

func IOSStreamMJPEGWda(c *gin.Context) {
	udid := c.Param("udid")
	platDev, ok := devices.DevManager.Get(udid)
	if !ok {
		logger.ProviderLogger.LogError("IOSStreamMJPEGWda", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	iosDev, ok2 := platDev.(*devices.IOSDevice)
	if !ok2 {
		logger.ProviderLogger.LogError("IOSStreamMJPEGWda", fmt.Sprintf("Device `%s` is not an iOS device", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	streamURL := "http://localhost:" + iosDev.GetWDAStreamPort()
	err := proxyIOSMJPEGStream(c.Request.Context(), c.Writer, streamURL, iosMJPEGHTTPClient, defaultIOSMJPEGProxyOptions)
	if err == nil || errors.Is(err, context.Canceled) || c.Request.Context().Err() != nil {
		return
	}

	logger.ProviderLogger.LogWarn("IOSStreamMJPEGWda", fmt.Sprintf("Stopped WDA MJPEG stream for device %s: %s", udid, err))
	if !c.Writer.Written() {
		c.AbortWithStatus(http.StatusBadGateway)
	}
}

func proxyIOSMJPEGStream(ctx context.Context, destination http.ResponseWriter, streamURL string, client *http.Client, options iosMJPEGProxyOptions) error {
	if options.copyBufferSize <= 0 {
		return errors.New("iOS MJPEG copy buffer size must be positive")
	}
	if options.maxFrameBytes <= 0 {
		return errors.New("iOS MJPEG max frame size must be positive")
	}

	flusher, ok := destination.(http.Flusher)
	if !ok {
		return errors.New("iOS MJPEG destination does not support flushing")
	}

	resp, reader, err := openIOSMJPEGMultipart(ctx, streamURL, client)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	destination.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	destination.Header().Set("Cache-Control", "no-store")
	destination.WriteHeader(http.StatusOK)

	copyBuffer := make([]byte, options.copyBufferSize)
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return nil
		}
		if nextErr != nil {
			return fmt.Errorf("read WDA MJPEG multipart boundary: %w", nextErr)
		}

		if _, err = io.WriteString(destination, "\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n"); err != nil {
			return fmt.Errorf("write MJPEG frame header: %w", err)
		}

		frameBytes, copyErr := copyBoundedMJPEGFrame(destination, part, copyBuffer, options.maxFrameBytes)
		if copyErr != nil {
			return fmt.Errorf("copy WDA MJPEG frame after %d bytes: %w", frameBytes, copyErr)
		}
		if closeErr := part.Close(); closeErr != nil {
			return fmt.Errorf("close WDA MJPEG frame after %d bytes: %w", frameBytes, closeErr)
		}
		flusher.Flush()
	}
}

func openIOSMJPEGMultipart(ctx context.Context, streamURL string, client *http.Client) (*http.Response, *multipart.Reader, error) {
	if client == nil {
		return nil, nil, errors.New("iOS MJPEG HTTP client is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create WDA MJPEG request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("open WDA MJPEG stream: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("WDA MJPEG stream returned HTTP %d", resp.StatusCode)
	}

	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("parse WDA MJPEG content type: %w", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("WDA MJPEG stream returned unsupported content type %q", mediaType)
	}

	boundary := strings.TrimPrefix(strings.TrimSpace(params["boundary"]), "--")
	if boundary == "" {
		resp.Body.Close()
		return nil, nil, errors.New("WDA MJPEG stream response has no multipart boundary")
	}

	return resp, multipart.NewReader(resp.Body, boundary), nil
}

func copyBoundedMJPEGFrame(destination io.Writer, source io.Reader, buffer []byte, maxFrameBytes int64) (int64, error) {
	if len(buffer) == 0 {
		return 0, errors.New("MJPEG copy buffer is empty")
	}
	if maxFrameBytes <= 0 {
		return 0, errors.New("MJPEG frame limit must be positive")
	}

	var written int64
	zeroReads := 0
	for {
		readSize := len(buffer)
		remaining := maxFrameBytes - written
		if remaining < int64(readSize) {
			readSize = int(remaining) + 1
		}

		readBytes, readErr := source.Read(buffer[:readSize])
		if readBytes > 0 {
			zeroReads = 0
			if written+int64(readBytes) > maxFrameBytes {
				return written, fmt.Errorf("%w: limit=%d observed_at_least=%d", errIOSMJPEGFrameTooLarge, maxFrameBytes, maxFrameBytes+1)
			}
			writeBytes, writeErr := destination.Write(buffer[:readBytes])
			written += int64(writeBytes)
			if writeErr != nil {
				return written, writeErr
			}
			if writeBytes != readBytes {
				return written, io.ErrShortWrite
			}
		} else if readErr == nil {
			zeroReads++
			if zeroReads >= 100 {
				return written, io.ErrNoProgress
			}
		}

		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func IosStreamProxyGADS(c *gin.Context) {
	udid := c.Param("udid")
	platDev, deviceFound := devices.DevManager.Get(udid)
	if !deviceFound {
		logger.ProviderLogger.LogError("IosStreamProxyGADS", fmt.Sprintf("Device with UDID `%s` not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	rcDev, isRcDevice := platDev.(devices.RemoteControllable)
	if !isRcDevice {
		logger.ProviderLogger.LogError("IosStreamProxyGADS", fmt.Sprintf("Device `%s` does not support streaming", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	jpegChannel := make(chan []byte, 15)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the new conn
	wsConn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logger.ProviderLogger.LogError("ios_stream", fmt.Sprintf("Failed to upgrade http conn to ws when starting streaming for device `%s` - %s", udid, err))
		return
	}

	// Read data from device
	server := "localhost:" + rcDev.GetStreamPort()
	// Connect to the server
	conn, err := net.Dial("tcp", server)
	if err != nil {
		fmt.Println("Error connecting:", err.Error())
		os.Exit(1)
	}

	defer func() {
		err := wsConn.Close()
		if err != nil {
			logger.ProviderLogger.LogError("ios_stream", fmt.Sprintf("Failed to close websocket connection when finishing streaming for device `%s` - %s", udid, err))
		}
		err = conn.Close()
		if err != nil {
			logger.ProviderLogger.LogError("ios_stream", fmt.Sprintf("Failed to close broadcast TCP connection when finishing streaming for device `%s` - %s", udid, err))
		}
		close(jpegChannel)
	}()

	// Get data from the jpeg channel and send it over the ws
	// The channel will act as a buffer for slower consumer because this could crash the broadcast app
	// Or at least I assume this is the problem in long distance connections
	go func() {
		for {
			select {
			case jpegImage := <-jpegChannel:
				// Send the jpeg over the websocket
				err = wsutil.WriteServerBinary(wsConn, jpegImage)
				if err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	var buffer []byte
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read data from the connection
			tempBuf := make([]byte, 1024)
			n, err := conn.Read(tempBuf)
			if err != nil {
				if err != io.EOF {
					return
				}
				break
			}

			// Append the read bytes to the buffer
			buffer = append(buffer, tempBuf[:n]...)

			// Check if buffer has a complete JPEG image
			start, end := findJPEGMarkers(buffer)
			if start >= 0 && end > start {
				// Process the JPEG image
				jpegImage := buffer[start : end+2] // Include end marker
				// Keep any remaining data in the buffer for the next image
				buffer = buffer[end+2:]
				// Send the jpeg to the channel
				jpegChannel <- jpegImage
			}
		}
	}
}

func IosStreamProxyWDA(c *gin.Context) {
	udid := c.Param("udid")
	platDev, ok := devices.DevManager.Get(udid)
	if !ok {
		logger.ProviderLogger.LogError("IosStreamProxyWDA", fmt.Sprintf("Device with UDID %s not found", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	iosDev, ok2 := platDev.(*devices.IOSDevice)
	if !ok2 {
		logger.ProviderLogger.LogError("IosStreamProxyWDA", fmt.Sprintf("Device %s is not an iOS device", udid))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	conn, _, _, err := ws.UpgradeHTTP(c.Request, c.Writer)
	if err != nil {
		logger.ProviderLogger.LogError("IosStreamProxyWDA", fmt.Sprintf("Failed upgrading HTTP to WebSocket for device %s: %s", udid, err))
		return
	}
	defer conn.Close()

	streamURL := "http://localhost:" + iosDev.GetWDAStreamPort()
	err = proxyIOSMJPEGWebSocket(c.Request.Context(), conn, streamURL, iosMJPEGHTTPClient, defaultIOSMJPEGProxyOptions)
	if err == nil || errors.Is(err, context.Canceled) || c.Request.Context().Err() != nil {
		return
	}
	logger.ProviderLogger.LogWarn("IosStreamProxyWDA", fmt.Sprintf("Stopped WDA MJPEG WebSocket stream for device %s: %s", udid, err))
}

func proxyIOSMJPEGWebSocket(ctx context.Context, destination io.Writer, streamURL string, client *http.Client, options iosMJPEGProxyOptions) error {
	if options.copyBufferSize <= 0 {
		return errors.New("iOS MJPEG copy buffer size must be positive")
	}
	if options.maxFrameBytes <= 0 {
		return errors.New("iOS MJPEG max frame size must be positive")
	}

	resp, reader, err := openIOSMJPEGMultipart(ctx, streamURL, client)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	copyBuffer := make([]byte, options.copyBufferSize)
	websocketWriter := wsutil.NewWriterBufferSize(destination, ws.StateServerSide, ws.OpBinary, options.copyBufferSize)
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return nil
		}
		if nextErr != nil {
			return fmt.Errorf("read WDA MJPEG multipart boundary: %w", nextErr)
		}

		frameBytes, copyErr := copyBoundedMJPEGFrame(websocketWriter, part, copyBuffer, options.maxFrameBytes)
		if copyErr != nil {
			return fmt.Errorf("copy WDA MJPEG WebSocket frame after %d bytes: %w", frameBytes, copyErr)
		}
		if closeErr := part.Close(); closeErr != nil {
			return fmt.Errorf("close WDA MJPEG WebSocket frame after %d bytes: %w", frameBytes, closeErr)
		}
		if flushErr := websocketWriter.Flush(); flushErr != nil {
			return fmt.Errorf("flush WDA MJPEG WebSocket frame after %d bytes: %w", frameBytes, flushErr)
		}
	}
}
