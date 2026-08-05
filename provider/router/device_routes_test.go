package router

import (
	"GADS/common/models"
	"GADS/provider/devices"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type noopTestLogger struct{}

func (noopTestLogger) LogDebug(eventName string, message string) {}
func (noopTestLogger) LogInfo(eventName string, message string)  {}
func (noopTestLogger) LogError(eventName string, message string) {}
func (noopTestLogger) LogWarn(eventName string, message string)  {}
func (noopTestLogger) LogFatal(eventName string, message string) {}
func (noopTestLogger) LogPanic(eventName string, message string) {}

func TestDecodeClipboardResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "base64 plaintext",
			body: `{"value":"aGVsbG8="}`,
			want: "hello",
		},
		{
			name: "empty clipboard",
			body: `{"value":""}`,
			want: "",
		},
		{
			name: "raw plaintext fallback",
			body: `{"value":"not base64 text!"}`,
			want: "not base64 text!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeClipboardResponse([]byte(tt.body))
			if err != nil {
				t.Fatalf("decodeClipboardResponse returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeClipboardResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeClipboardResponseRejectsInvalidJSON(t *testing.T) {
	if _, err := decodeClipboardResponse([]byte(`not-json`)); err == nil {
		t.Fatal("decodeClipboardResponse should reject invalid JSON")
	}
}

func TestSuccessfulClipboardResponseRequiresNonEmptyValue(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"value":""}`)),
	}

	if _, err := successfulClipboardResponse(resp, true); err == nil {
		t.Fatal("successfulClipboardResponse should reject an empty required clipboard value")
	}
}

func TestSuccessfulClipboardResponseAllowsEmptyValueWhenOptional(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"value":""}`)),
	}

	checkedResp, err := successfulClipboardResponse(resp, false)
	if err != nil {
		t.Fatalf("successfulClipboardResponse should allow optional empty value: %v", err)
	}
	body, err := io.ReadAll(checkedResp.Body)
	if err != nil {
		t.Fatalf("failed to read restored response body: %v", err)
	}
	if string(body) != `{"value":""}` {
		t.Fatalf("restored body = %q", body)
	}
}

func TestSuccessfulClipboardResponsePreservesBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"value":"aGVsbG8="}`)),
	}

	checkedResp, err := successfulClipboardResponse(resp, true)
	if err != nil {
		t.Fatalf("successfulClipboardResponse returned error: %v", err)
	}
	body, err := io.ReadAll(checkedResp.Body)
	if err != nil {
		t.Fatalf("failed to read restored response body: %v", err)
	}
	if string(body) != `{"value":"aGVsbG8="}` {
		t.Fatalf("restored body = %q", body)
	}
}

func TestPastePermissionAlertMessageClassification(t *testing.T) {
	if !isPastePermissionAlertMessage(`WebDriverAgentRunner-Runner would like to paste from Safari`) {
		t.Fatal("expected paste permission alert text to be classified")
	}
	if !isPastePermissionAlertMessage(`Allow Paste`) {
		t.Fatal("expected Allow Paste button text to be classified")
	}
	if !isPastePermissionAlertMessage(`WebDriverAgentRunner-Runner想从“Safari”粘贴`) {
		t.Fatal("expected simplified Chinese paste permission alert text to be classified")
	}
	if !isPastePermissionAlertMessage(`允許貼上`) {
		t.Fatal("expected traditional Chinese paste permission button text to be classified")
	}
	if isPastePermissionAlertMessage(`connection reset by peer`) {
		t.Fatal("expected transport failure text not to be classified as paste alert")
	}
}

func TestAcceptIOSPasteAlertFallsBackToSessionButton(t *testing.T) {
	acceptedNames := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{
				"sessionId": "session-1",
				"value": map[string]any{
					"sessionId": "session-1",
				},
			})
		case "/alert/text":
			writeWDANoSuchAlert(t, w)
		case "/session/session-1/alert/text":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{
				"value": "WebDriverAgentRunner-Runner would like to paste from Safari",
			})
		case "/alert/accept":
			writeWDANoSuchAlert(t, w)
		case "/session/session-1/alert/accept":
			var payload struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode alert accept payload: %v", err)
			}
			acceptedNames = append(acceptedNames, payload.Name)
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"value": nil})
		default:
			t.Fatalf("unexpected WDA test path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	dev := newWDATestIOSDevice(t, server)
	if err := acceptIOSPasteAlertWithClient(server.Client(), dev); err != nil {
		t.Fatalf("acceptIOSPasteAlertWithClient returned error: %v", err)
	}
	if len(acceptedNames) != 1 || acceptedNames[0] != "Allow Paste" {
		t.Fatalf("accepted names = %#v, want [Allow Paste]", acceptedNames)
	}
}

func TestAcceptIOSPasteAlertTriesPasteButtonsWhenTextIsUnavailable(t *testing.T) {
	acceptedNames := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"sessionId": "session-1"})
		case "/alert/text", "/session/session-1/alert/text":
			writeWDATestJSON(t, w, http.StatusInternalServerError, map[string]any{
				"value": map[string]any{"message": "alert text temporarily unavailable"},
			})
		case "/alert/accept", "/session/session-1/alert/accept":
			var payload struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode alert accept payload: %v", err)
			}
			acceptedNames = append(acceptedNames, payload.Name)
			if payload.Name == "Allow Paste" {
				writeWDATestJSON(t, w, http.StatusOK, map[string]any{"value": nil})
				return
			}
			writeWDANoSuchAlert(t, w)
		default:
			t.Fatalf("unexpected WDA test path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	dev := newWDATestIOSDevice(t, server)
	if err := acceptIOSPasteAlertWithClient(server.Client(), dev); err != nil {
		t.Fatalf("acceptIOSPasteAlertWithClient returned error: %v", err)
	}
	if len(acceptedNames) == 0 || acceptedNames[0] != "Allow Paste" {
		t.Fatalf("accepted names = %#v, want first Allow Paste", acceptedNames)
	}
}

func TestAcceptIOSPasteAlertDoesNotAcceptWhenNoAlertExists(t *testing.T) {
	acceptCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"sessionId": "session-1"})
		case "/alert/text", "/session/session-1/alert/text":
			writeWDANoSuchAlert(t, w)
		case "/alert/accept", "/session/session-1/alert/accept":
			acceptCalled = true
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"value": nil})
		default:
			t.Fatalf("unexpected WDA test path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	dev := newWDATestIOSDevice(t, server)
	if err := acceptIOSPasteAlertWithClient(server.Client(), dev); err == nil {
		t.Fatal("acceptIOSPasteAlertWithClient should reject missing alerts")
	}
	if acceptCalled {
		t.Fatal("acceptIOSPasteAlertWithClient should not call alert/accept when WDA reports no such alert")
	}
}

func TestAcceptIOSPasteAlertRejectsNonPasteAlert(t *testing.T) {
	acceptCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alert/text":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"value": "Low Battery"})
		case "/alert/accept", "/session/session-1/alert/accept":
			acceptCalled = true
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"value": nil})
		case "/status":
			writeWDATestJSON(t, w, http.StatusOK, map[string]any{"sessionId": "session-1"})
		default:
			t.Fatalf("unexpected WDA test path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	dev := newWDATestIOSDevice(t, server)
	if err := acceptIOSPasteAlertWithClient(server.Client(), dev); err == nil {
		t.Fatal("acceptIOSPasteAlertWithClient should reject non-paste alerts")
	}
	if acceptCalled {
		t.Fatal("acceptIOSPasteAlertWithClient should not accept non-paste alerts")
	}
}

func newWDATestIOSDevice(t *testing.T, server *httptest.Server) *devices.IOSDevice {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse WDA test server port: %v", err)
	}
	return &devices.IOSDevice{
		RuntimeState: devices.RuntimeState{
			DBDevice: models.DBDevice{
				UDID: "test-ios-device",
				OS:   "ios",
			},
			Context: context.Background(),
			Logger:  noopTestLogger{},
		},
		WDAPort: port,
	}
}

func writeWDATestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("failed to write WDA test response: %v", err)
	}
}

func writeWDANoSuchAlert(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeWDATestJSON(t, w, http.StatusNotFound, map[string]any{
		"value": map[string]any{
			"error":   "no such alert",
			"message": "An attempt was made to operate on a modal dialog when one was not open",
		},
	})
}

func TestWDAStallErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context deadline",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "client timeout",
			err:  errors.New("Post \"http://localhost:8100/wda/getPasteboard\": net/http: request canceled (Client.Timeout exceeded while awaiting headers)"),
			want: true,
		},
		{
			name: "connection refused",
			err:  errors.New("dial tcp 127.0.0.1:8100: connect: connection refused"),
			want: true,
		},
		{
			name: "connection reset",
			err:  errors.New("read tcp 127.0.0.1:51000->127.0.0.1:8100: read: connection reset by peer"),
			want: true,
		},
		{
			name: "eof",
			err:  io.EOF,
			want: true,
		},
		{
			name: "paste alert is not stall",
			err:  errors.New(`WebDriverAgentRunner-Runner would like to paste from Safari`),
			want: false,
		},
		{
			name: "ordinary wda error",
			err:  errors.New("no such alert"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWDAStallError(tt.err); got != tt.want {
				t.Fatalf("isWDAStallError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldRestoreIOSForegroundBundle(t *testing.T) {
	tests := []struct {
		name     string
		bundleID string
		want     bool
	}{
		{
			name:     "user app",
			bundleID: "com.apple.mobilesafari",
			want:     true,
		},
		{
			name:     "springboard",
			bundleID: "com.apple.springboard",
			want:     true,
		},
		{
			name:     "empty",
			bundleID: "",
			want:     false,
		},
		{
			name:     "xct runner",
			bundleID: "com.codeyee.tempwda.xctrunner",
			want:     false,
		},
		{
			name:     "webdriveragent bundle",
			bundleID: "com.facebook.WebDriverAgentRunner",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRestoreIOSForegroundBundle(tt.bundleID); got != tt.want {
				t.Fatalf("shouldRestoreIOSForegroundBundle(%q) = %v, want %v", tt.bundleID, got, tt.want)
			}
		})
	}
}

func TestIsSupportedStreamTypeUsesProvidedTypes(t *testing.T) {
	supported := []models.StreamType{models.MJPEGStreamType}

	if !isSupportedStreamType(models.MJPEGStreamTypeId, "ios", supported) {
		t.Fatal("expected MJPEG to be supported")
	}
	if isSupportedStreamType(models.IOSWebRTCBroadcastExtensionId, "ios", supported) {
		t.Fatal("expected Broadcast to be rejected when it is not in the provided supported list")
	}
}

func TestIsSupportedStreamTypeFallsBackToOSDefaults(t *testing.T) {
	if !isSupportedStreamType(models.IOSWebRTCBroadcastExtensionId, "ios", nil) {
		t.Fatal("expected iOS Broadcast to be supported by default for iOS")
	}
	if isSupportedStreamType(models.IOSWebRTCBroadcastExtensionId, "android", nil) {
		t.Fatal("expected iOS Broadcast to be rejected for Android defaults")
	}
}
