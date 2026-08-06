package router

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestCopyBoundedMJPEGFrame(t *testing.T) {
	payload := []byte("jpeg-frame-payload")
	var destination bytes.Buffer

	written, err := copyBoundedMJPEGFrame(&destination, bytes.NewReader(payload), make([]byte, 5), int64(len(payload)))
	if err != nil {
		t.Fatalf("copyBoundedMJPEGFrame returned error: %v", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("written = %d, want %d", written, len(payload))
	}
	if !bytes.Equal(destination.Bytes(), payload) {
		t.Fatalf("destination = %q, want %q", destination.Bytes(), payload)
	}
}

func TestCopyBoundedMJPEGFrameRejectsOversizedPart(t *testing.T) {
	payload := []byte("0123456789")
	var destination bytes.Buffer

	written, err := copyBoundedMJPEGFrame(&destination, bytes.NewReader(payload), make([]byte, 4), 8)
	if !errors.Is(err, errIOSMJPEGFrameTooLarge) {
		t.Fatalf("error = %v, want errIOSMJPEGFrameTooLarge", err)
	}
	if written > 8 || int64(destination.Len()) > 8 {
		t.Fatalf("oversized frame wrote %d bytes with destination length %d", written, destination.Len())
	}
}

func TestCopyBoundedMJPEGFrameHasNoPerFrameAllocation(t *testing.T) {
	payload := bytes.Repeat([]byte{0x7f}, 256*1024)
	reader := bytes.NewReader(payload)
	buffer := make([]byte, 64*1024)

	allocations := testing.AllocsPerRun(100, func() {
		reader.Reset(payload)
		written, err := copyBoundedMJPEGFrame(io.Discard, reader, buffer, int64(len(payload)))
		if err != nil || written != int64(len(payload)) {
			panic("unexpected bounded copy result")
		}
	})
	if allocations > 0 {
		t.Fatalf("allocations per frame = %.2f, want 0", allocations)
	}
}

func TestProxyIOSMJPEGStreamForwardsMultipartFrames(t *testing.T) {
	frames := [][]byte{[]byte("frame-one"), []byte("frame-two")}
	server := newMJPEGTestServer(t, frames)
	defer server.Close()

	recorder := httptest.NewRecorder()
	err := proxyIOSMJPEGStream(context.Background(), recorder, server.URL, server.Client(), iosMJPEGProxyOptions{
		copyBufferSize: 4,
		maxFrameBytes:  1024,
	})
	if err != nil {
		t.Fatalf("proxyIOSMJPEGStream returned error: %v", err)
	}
	if recorder.Header().Get("Content-Type") != "multipart/x-mixed-replace; boundary=frame" {
		t.Fatalf("content type = %q", recorder.Header().Get("Content-Type"))
	}
	if !recorder.Flushed {
		t.Fatal("proxy did not flush completed frames")
	}
	for _, frame := range frames {
		if !bytes.Contains(recorder.Body.Bytes(), frame) {
			t.Fatalf("proxy output does not contain frame %q", frame)
		}
	}
	if got := strings.Count(recorder.Body.String(), "--frame"); got != len(frames) {
		t.Fatalf("downstream frame boundaries = %d, want %d", got, len(frames))
	}

	closedBody := append(bytes.Clone(recorder.Body.Bytes()), []byte("\r\n--frame--\r\n")...)
	reader := multipart.NewReader(bytes.NewReader(closedBody), "frame")
	for index, want := range frames {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("reading downstream part %d returned error: %v", index, err)
		}
		got, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("reading downstream frame %d returned error: %v", index, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("downstream frame %d = %q, want %q", index, got, want)
		}
	}
}

func TestProxyIOSMJPEGStreamRejectsOversizedFrame(t *testing.T) {
	server := newMJPEGTestServer(t, [][]byte{[]byte("0123456789")})
	defer server.Close()

	recorder := httptest.NewRecorder()
	err := proxyIOSMJPEGStream(context.Background(), recorder, server.URL, server.Client(), iosMJPEGProxyOptions{
		copyBufferSize: 4,
		maxFrameBytes:  8,
	})
	if !errors.Is(err, errIOSMJPEGFrameTooLarge) {
		t.Fatalf("error = %v, want errIOSMJPEGFrameTooLarge", err)
	}
	if recorder.Body.Len() > 256 {
		t.Fatalf("oversized frame produced unexpectedly large output: %d bytes", recorder.Body.Len())
	}
}

func TestProxyIOSMJPEGStreamRejectsInvalidUpstreamBeforeWriting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "not multipart")
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	err := proxyIOSMJPEGStream(context.Background(), recorder, server.URL, server.Client(), defaultIOSMJPEGProxyOptions)
	if err == nil {
		t.Fatal("proxyIOSMJPEGStream should reject a non-multipart response")
	}
	if recorder.Header().Get("Content-Type") != "" || recorder.Body.Len() != 0 {
		t.Fatalf("proxy committed downstream output before validation: header=%q body=%d", recorder.Header().Get("Content-Type"), recorder.Body.Len())
	}
}

func TestProxyIOSMJPEGStreamTimesOutStalledRead(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=stall")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newIOSMJPEGHTTPClient(time.Second, 50*time.Millisecond)
	defer client.CloseIdleConnections()
	recorder := httptest.NewRecorder()
	start := time.Now()
	err := proxyIOSMJPEGStream(context.Background(), recorder, server.URL, client, defaultIOSMJPEGProxyOptions)
	if err == nil {
		t.Fatal("proxyIOSMJPEGStream should stop a stalled upstream read")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stalled read took %s, want under 1s", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("test server did not start the stalled response")
	}
}

func TestProxyIOSMJPEGStreamPropagatesDownstreamCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=stall")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newIOSMJPEGHTTPClient(time.Second, 5*time.Second)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- proxyIOSMJPEGStream(ctx, httptest.NewRecorder(), server.URL, client, defaultIOSMJPEGProxyOptions)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("test server did not start the response")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop after downstream cancellation")
	}
}

func TestProxyIOSMJPEGWebSocketForwardsFrames(t *testing.T) {
	frames := [][]byte{[]byte("frame-one"), []byte("frame-two")}
	server := newMJPEGTestServer(t, frames)
	defer server.Close()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		done <- proxyIOSMJPEGWebSocket(context.Background(), serverConn, server.URL, server.Client(), iosMJPEGProxyOptions{
			copyBufferSize: 4,
			maxFrameBytes:  1024,
		})
	}()

	for index, want := range frames {
		payload, op, err := wsutil.ReadServerData(clientConn)
		if err != nil {
			t.Fatalf("reading WebSocket frame %d returned error: %v", index, err)
		}
		if op != ws.OpBinary {
			t.Fatalf("WebSocket frame %d opcode = %s, want binary", index, op)
		}
		if !bytes.Equal(payload, want) {
			t.Fatalf("WebSocket frame %d = %q, want %q", index, payload, want)
		}
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxyIOSMJPEGWebSocket returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket proxy did not finish after upstream EOF")
	}
}

func newMJPEGTestServer(t *testing.T, frames [][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := multipart.NewWriter(w)
		if err := writer.SetBoundary("test-boundary"); err != nil {
			t.Fatalf("SetBoundary returned error: %v", err)
		}
		w.Header().Set("Content-Type", writer.FormDataContentType())
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", "image/jpeg")
			part, err := writer.CreatePart(header)
			if err != nil {
				t.Fatalf("CreatePart returned error: %v", err)
			}
			if _, err = part.Write(frame); err != nil {
				t.Fatalf("writing test frame returned error: %v", err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("closing multipart writer returned error: %v", err)
		}
	}))
}
