package router

import (
	"GADS/common/models"
	"GADS/common/utils"
	"GADS/provider/config"
	"GADS/provider/devices"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/danielpaulus/go-ios/ios/instruments"
)

var controlNetClient = &http.Client{
	Timeout: time.Second * 120,
}

var controlAppiumSessionLocks sync.Map

const (
	iosControlSwipeVelocity     = 2500
	iosControlDragVelocity      = 2500
	iosControlDragPressDuration = 0.03
	iosControlDragHoldDuration  = 0.02
	iosClipboardDirectTimeout   = 2 * time.Second
	iosClipboardFallbackTimeout = 6 * time.Second
	iosPasteAlertPollInterval   = 250 * time.Millisecond
	iosPasteAlertRequestTimeout = 1 * time.Second
	iosPasteAlertAcceptTimeout  = 5 * time.Second
)

func androidRemoteServerRequest(dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	andDev, ok := dev.(*devices.AndroidDevice)
	if !ok {
		return nil, fmt.Errorf("device %s is not an Android device", dev.GetUDID())
	}
	url := fmt.Sprintf("http://localhost:%s/%s", andDev.GetAndroidRemoteServerPort(), endpoint)
	dev.GetLogger().LogDebug("androidRemoteServerRequest", fmt.Sprintf("Calling `%s` for device `%s`", url, dev.GetUDID()))
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return nil, err
	}
	return controlNetClient.Do(req)
}

func androidRemoteServerRequestJson(dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	andDev, ok := dev.(*devices.AndroidDevice)
	if !ok {
		return nil, fmt.Errorf("device %s is not an Android device", dev.GetUDID())
	}
	url := fmt.Sprintf("http://localhost:%s/%s", andDev.GetAndroidRemoteServerPort(), endpoint)
	dev.GetLogger().LogDebug("androidRemoteServerRequest", fmt.Sprintf("Calling `%s` for device `%s`", url, dev.GetUDID()))
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return controlNetClient.Do(req)
}

func appiumRequest(dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	return appiumRequestForSession(dev, dev.GetAppiumSessionID(), method, endpoint, requestBody)
}

func appiumRequestForSession(dev devices.PlatformDevice, sessionID, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/session/%s/%s", dev.GetAppiumPort(), sessionID, endpoint)
	dev.GetLogger().LogDebug("appium_interact", fmt.Sprintf("Calling `%s` for device `%s`", url, dev.GetUDID()))
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return nil, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return controlNetClient.Do(req)
}

func appiumRequestNoSession(dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/%s", dev.GetAppiumPort(), endpoint)
	dev.GetLogger().LogDebug("appium_interact", fmt.Sprintf("Calling `%s` for device `%s`", url, dev.GetUDID()))
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return nil, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return controlNetClient.Do(req)
}

func appiumExecuteScript(dev devices.PlatformDevice, script string, args []map[string]any) (*http.Response, error) {
	return appiumExecuteScriptForSession(dev, dev.GetAppiumSessionID(), script, args)
}

func appiumExecuteScriptForSession(dev devices.PlatformDevice, sessionID, script string, args []map[string]any) (*http.Response, error) {
	requestBody := map[string]any{
		"script": script,
		"args":   args,
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return nil, err
	}
	return appiumRequestForSession(dev, sessionID, http.MethodPost, "execute/sync", bytes.NewReader(actionJSON))
}

func wdaRequest(dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	return wdaRequestWithClient(controlNetClient, dev, method, endpoint, requestBody)
}

func wdaRequestWithClient(client *http.Client, dev devices.PlatformDevice, method, endpoint string, requestBody io.Reader) (*http.Response, error) {
	iosDev, ok := dev.(*devices.IOSDevice)
	if !ok {
		return nil, fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
	}
	if client == nil {
		client = controlNetClient
	}
	url := fmt.Sprintf("http://localhost:%v/%s", iosDev.GetWDAPort(), endpoint)
	dev.GetLogger().LogDebug("wda_interact", fmt.Sprintf("Calling `%s` for device `%s`", url, dev.GetUDID()))
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return nil, err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return client.Do(req)
}

func wdaSessionRequest(dev devices.PlatformDevice, method, sessionID, endpoint string, requestBody io.Reader) (*http.Response, error) {
	return wdaSessionRequestWithClient(controlNetClient, dev, method, sessionID, endpoint, requestBody)
}

func wdaSessionRequestWithClient(client *http.Client, dev devices.PlatformDevice, method, sessionID, endpoint string, requestBody io.Reader) (*http.Response, error) {
	return wdaRequestWithClient(client, dev, method, fmt.Sprintf("session/%s/%s", sessionID, endpoint), requestBody)
}

func restoreResponseBody(resp *http.Response, body []byte) *http.Response {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp
}

func ensureSuccessfulResponse(resp *http.Response, action string) error {
	if resp == nil {
		return fmt.Errorf("%s failed: empty response", action)
	}
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return fmt.Errorf("%s failed with status %d", action, resp.StatusCode)
	}
	resp.Body.Close()

	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("%s failed with status %d", action, resp.StatusCode)
	}
	return fmt.Errorf("%s failed with status %d: %s", action, resp.StatusCode, message)
}

func shouldFallbackToWDASession(resp *http.Response, body []byte) bool {
	if resp.StatusCode == http.StatusNotFound {
		return true
	}
	responseText := strings.ToLower(string(body))
	return strings.Contains(responseText, "unknown command") || strings.Contains(responseText, "endpoint missing")
}

func shouldRefreshWDASession(resp *http.Response, body []byte) bool {
	if resp.StatusCode < http.StatusBadRequest {
		return false
	}
	responseText := strings.ToLower(string(body))
	return strings.Contains(responseText, "invalid session id") ||
		strings.Contains(responseText, "session does not exist") ||
		strings.Contains(responseText, "session not created")
}

func getOrCreateWDASessionID(dev devices.PlatformDevice) (string, error) {
	iosDev, ok := dev.(*devices.IOSDevice)
	if !ok {
		return "", fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
	}
	return iosDev.EnsureWDASessionID()
}

func refreshWDASessionID(dev devices.PlatformDevice) (string, error) {
	iosDev, ok := dev.(*devices.IOSDevice)
	if !ok {
		return "", fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
	}
	return iosDev.RefreshWDASessionID()
}

func wdaSessionRequestWithRetry(dev devices.PlatformDevice, method, endpoint string, requestBody []byte) (*http.Response, error) {
	return wdaSessionRequestWithRetryUsingClient(controlNetClient, dev, method, endpoint, requestBody)
}

func wdaSessionRequestWithRetryUsingClient(client *http.Client, dev devices.PlatformDevice, method, endpoint string, requestBody []byte) (*http.Response, error) {
	sessionID, err := getOrCreateWDASessionID(dev)
	if err != nil {
		return nil, err
	}

	resp, err := wdaSessionRequestWithClient(client, dev, method, sessionID, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	resp.Body.Close()

	if !shouldRefreshWDASession(resp, body) {
		return restoreResponseBody(resp, body), nil
	}

	refreshedSessionID, err := refreshWDASessionID(dev)
	if err != nil {
		return nil, err
	}

	return wdaSessionRequestWithClient(client, dev, method, refreshedSessionID, endpoint, bytes.NewReader(requestBody))
}

func wdaRequestWithSessionFallback(dev devices.PlatformDevice, method, endpoint string, requestBody []byte, sessionEndpoint string, sessionRequestBody []byte) (*http.Response, error) {
	return wdaRequestWithSessionFallbackUsingClient(controlNetClient, dev, method, endpoint, requestBody, sessionEndpoint, sessionRequestBody)
}

func wdaRequestWithSessionFallbackUsingClient(client *http.Client, dev devices.PlatformDevice, method, endpoint string, requestBody []byte, sessionEndpoint string, sessionRequestBody []byte) (*http.Response, error) {
	resp, err := wdaRequestWithClient(client, dev, method, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	resp.Body.Close()

	if !shouldFallbackToWDASession(resp, body) {
		return restoreResponseBody(resp, body), nil
	}

	return wdaSessionRequestWithRetryUsingClient(client, dev, method, sessionEndpoint, sessionRequestBody)
}

func inferDirectionalSwipe(x, y, endX, endY float64) (string, bool) {
	deltaX := endX - x
	deltaY := endY - y
	absDeltaX := math.Abs(deltaX)
	absDeltaY := math.Abs(deltaY)

	// 仅把位移明显且主方向清晰的拖拽改走 direction 路由，斜向拖拽仍保留坐标拖拽。
	if math.Max(absDeltaX, absDeltaY) < 80 {
		return "", false
	}

	if absDeltaX >= absDeltaY*1.25 {
		if deltaX > 0 {
			return "right", true
		}
		return "left", true
	}

	if absDeltaY >= absDeltaX*1.25 {
		if deltaY > 0 {
			return "down", true
		}
		return "up", true
	}

	return "", false
}

func tryWDADirectionalSwipe(dev devices.PlatformDevice, direction string) (*http.Response, error) {
	requestBody := struct {
		Direction string `json:"direction"`
	}{
		Direction: direction,
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return nil, err
	}

	resp, err := wdaSessionRequestWithRetry(dev, http.MethodPost, "wda/swipe", actionJSON)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("directional WDA swipe failed with status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("directional WDA swipe failed with status %d: %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

func shouldRefreshAppiumSession(resp *http.Response, body []byte) bool {
	if resp == nil || resp.StatusCode < http.StatusBadRequest {
		return false
	}
	responseText := strings.ToLower(string(body))
	return strings.Contains(responseText, "invalid session id") ||
		strings.Contains(responseText, "a session is either terminated or not started") ||
		strings.Contains(responseText, "nosuchdrivererror") ||
		strings.Contains(responseText, "no such driver")
}

func clearStalePrimaryAppiumSession(dev devices.PlatformDevice, staleSessionID string) {
	staleSessionID = strings.TrimSpace(staleSessionID)
	if staleSessionID == "" || staleSessionID == strings.TrimSpace(dev.GetControlAppiumSessionID()) {
		return
	}
	if strings.TrimSpace(dev.GetAppiumSessionID()) != staleSessionID {
		return
	}
	dev.SetAppiumSessionID("")
	dev.SetHasAppiumSession(false)
	dev.SetAppiumLastPingTS(0)
}

func getControlAppiumSessionLock(dev devices.PlatformDevice) *sync.Mutex {
	value, _ := controlAppiumSessionLocks.LoadOrStore(dev.GetUDID(), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func deleteControlAppiumSession(dev devices.PlatformDevice) {
	lock := getControlAppiumSessionLock(dev)
	lock.Lock()
	defer lock.Unlock()

	sessionID := strings.TrimSpace(dev.GetControlAppiumSessionID())
	if sessionID == "" || dev.GetAppiumPort() == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://localhost:%s/session/%s", dev.GetAppiumPort(), sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Failed to prepare control Appium session delete for device `%s`: %v", dev.GetUDID(), err))
		dev.SetControlAppiumSessionID("")
		return
	}

	resp, err := controlNetClient.Do(req)
	if err != nil {
		dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Best-effort delete of control Appium session `%s` for device `%s` failed: %v", sessionID, dev.GetUDID(), err))
		dev.SetControlAppiumSessionID("")
		return
	}
	resp.Body.Close()
	dev.SetControlAppiumSessionID("")
}

func hasExternalAppiumSession(dev devices.PlatformDevice) bool {
	sessionID := strings.TrimSpace(dev.GetAppiumSessionID())
	if sessionID == "" {
		return false
	}
	return sessionID != strings.TrimSpace(dev.GetControlAppiumSessionID())
}

func restoreAppiumResponse(resp *http.Response) (*http.Response, bool, error) {
	if resp == nil {
		return nil, false, fmt.Errorf("empty Appium response")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, false, err
	}
	resp.Body.Close()
	restoredResp := restoreResponseBody(resp, body)
	if shouldRefreshAppiumSession(resp, body) {
		return restoredResp, true, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return restoredResp, false, fmt.Errorf("Appium request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return restoredResp, false, nil
}

func getOrCreateControlAppiumSessionID(dev devices.PlatformDevice) (string, error) {
	if dev.GetOS() != "ios" {
		return "", fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
	}

	lock := getControlAppiumSessionLock(dev)
	lock.Lock()
	defer lock.Unlock()

	if sessionID := strings.TrimSpace(dev.GetControlAppiumSessionID()); sessionID != "" {
		return sessionID, nil
	}
	return createControlAppiumSession(dev)
}

func createControlAppiumSession(dev devices.PlatformDevice) (string, error) {
	if dev.GetOS() != "ios" {
		return "", fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
	}
	if dev.GetAppiumPort() == "" || !dev.GetIsAppiumUp() {
		return "", fmt.Errorf("Appium is not ready for device %s", dev.GetUDID())
	}

	caps := map[string]any{
		"platformName":              "iOS",
		"appium:automationName":     "XCUITest",
		"appium:udid":               dev.GetUDID(),
		"appium:autoLaunch":         false,
		"appium:shouldTerminateApp": false,
		"appium:forceAppLaunch":     false,
		"appium:newCommandTimeout":  3600,
	}
	if iosDev, ok := dev.(*devices.IOSDevice); ok {
		caps["appium:webDriverAgentUrl"] = "http://localhost:" + iosDev.GetWDAPort()
	}

	requestBody := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": caps,
			"firstMatch":  []map[string]any{{}},
		},
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return "", err
	}

	resp, err := appiumRequestNoSession(dev, http.MethodPost, "session", bytes.NewReader(actionJSON))
	if err != nil {
		return "", err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		resp.Body.Close()
		return "", err
	}
	resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("creating control Appium session failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var sessionResp struct {
		SessionID string `json:"sessionId"`
		Value     struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &sessionResp); err != nil {
		return "", fmt.Errorf("parsing control Appium session response failed: %w", err)
	}

	sessionID := strings.TrimSpace(sessionResp.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(sessionResp.Value.SessionID)
	}
	if sessionID == "" {
		return "", fmt.Errorf("Appium createSession returned empty session id")
	}

	dev.SetControlAppiumSessionID(sessionID)
	dev.SetAppiumSessionID(sessionID)
	dev.SetHasAppiumSession(true)
	dev.SetAppiumLastPingTS(time.Now().UnixMilli())
	dev.GetLogger().LogInfo("appium_interact", fmt.Sprintf("Created control Appium session `%s` for device `%s`", sessionID, dev.GetUDID()))
	return sessionID, nil
}

func executeIOSSwipeViaAppiumSession(dev devices.PlatformDevice, sessionID string, x, y, endX, endY float64) (*http.Response, bool, error) {
	if direction, ok := inferDirectionalSwipe(x, y, endX, endY); ok {
		resp, err := appiumExecuteScriptForSession(dev, sessionID, "mobile: swipe", []map[string]any{{
			"direction": direction,
			"velocity":  iosControlSwipeVelocity,
		}})
		if err != nil {
			return nil, false, err
		}
		return restoreAppiumResponse(resp)
	}

	fromX, fromY, err := normalizeIOSPointForAppium(dev, x, y)
	if err != nil {
		return nil, false, err
	}
	toX, toY, err := normalizeIOSPointForAppium(dev, endX, endY)
	if err != nil {
		return nil, false, err
	}

	resp, err := appiumExecuteScriptForSession(dev, sessionID, "mobile: dragFromToWithVelocity", []map[string]any{{
		"fromX":         fromX,
		"fromY":         fromY,
		"toX":           toX,
		"toY":           toY,
		"pressDuration": iosControlDragPressDuration,
		"holdDuration":  iosControlDragHoldDuration,
		"velocity":      iosControlDragVelocity,
	}})
	if err != nil {
		return nil, false, err
	}
	return restoreAppiumResponse(resp)
}

func executeIOSSwipeViaControlSession(dev devices.PlatformDevice, x, y, endX, endY float64) (*http.Response, error) {
	sessionID, err := getOrCreateControlAppiumSessionID(dev)
	if err != nil {
		return nil, err
	}

	resp, shouldRefresh, err := executeIOSSwipeViaAppiumSession(dev, sessionID, x, y, endX, endY)
	if err != nil {
		return nil, err
	}
	if !shouldRefresh {
		return resp, nil
	}

	dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Control Appium session `%s` became invalid for device `%s`, recreating", sessionID, dev.GetUDID()))
	deleteControlAppiumSession(dev)

	sessionID, err = getOrCreateControlAppiumSessionID(dev)
	if err != nil {
		return nil, err
	}
	resp, _, err = executeIOSSwipeViaAppiumSession(dev, sessionID, x, y, endX, endY)
	return resp, err
}

func executeIOSScriptViaAppiumSession(dev devices.PlatformDevice, sessionID string, script string, args []map[string]any) (*http.Response, bool, error) {
	resp, err := appiumExecuteScriptForSession(dev, sessionID, script, args)
	if err != nil {
		return nil, false, err
	}
	return restoreAppiumResponse(resp)
}

func executeIOSScriptViaBestSession(dev devices.PlatformDevice, script string, args []map[string]any) (*http.Response, error) {
	if hasExternalAppiumSession(dev) {
		primarySessionID := dev.GetAppiumSessionID()
		resp, shouldRefresh, err := executeIOSScriptViaAppiumSession(dev, dev.GetAppiumSessionID(), script, args)
		if err != nil {
			return nil, err
		}
		if !shouldRefresh {
			return resp, nil
		}
		clearStalePrimaryAppiumSession(dev, primarySessionID)
		dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Primary Appium session `%s` is stale for device `%s`, falling back to control session for `%s`", primarySessionID, dev.GetUDID(), script))
	}

	if !dev.GetIsAppiumUp() {
		return nil, fmt.Errorf("Appium is not ready for device %s", dev.GetUDID())
	}

	sessionID, err := getOrCreateControlAppiumSessionID(dev)
	if err != nil {
		return nil, err
	}

	resp, shouldRefresh, err := executeIOSScriptViaAppiumSession(dev, sessionID, script, args)
	if err != nil {
		return nil, err
	}
	if !shouldRefresh {
		return resp, nil
	}

	dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Control Appium session `%s` became invalid for device `%s` while running `%s`, recreating", sessionID, dev.GetUDID(), script))
	deleteControlAppiumSession(dev)

	sessionID, err = getOrCreateControlAppiumSessionID(dev)
	if err != nil {
		return nil, err
	}
	resp, _, err = executeIOSScriptViaAppiumSession(dev, sessionID, script, args)
	return resp, err
}

func deviceLock(dev devices.PlatformDevice, lock string) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		return wdaRequest(dev, http.MethodPost, "wda/"+lock, nil)
	} else {
		return androidRemoteServerRequest(dev, http.MethodPost, lock, nil)
	}
}

func deviceTap(dev devices.PlatformDevice, x float64, y float64) (*http.Response, error) {
	requestBody := struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}{
		X: x,
		Y: y,
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return nil, err
	}

	if dev.GetOS() == "ios" {
		// Fast path：直连 WDA sessionless tap。配合 ios.go 的
		// snapshotMaxDepth=0/snapshotMaxChildren=1 设置，单次 tap 通常落在 10~30ms。
		// 浏览器/Hub 控制页直接发像素坐标，WDA 接受同一坐标空间，无需额外换算。
		resp, wdaErr := wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/tap", actionJSON, "wda/tap", actionJSON)
		if wdaErr == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		dev.GetLogger().LogWarn("wda_interact",
			fmt.Sprintf("WDA tap fast path failed for device `%s`, falling back to Appium: %v", dev.GetUDID(), wdaErr))

		// Fallback：Appium mobile:tap。在异常状态下更宽容，但延迟更高（~700ms+）。
		if hasExternalAppiumSession(dev) || dev.GetIsAppiumUp() {
			tapX, tapY, err := normalizeIOSPointForAppium(dev, x, y)
			if err != nil {
				return nil, err
			}
			appiumResp, err := executeIOSScriptViaBestSession(dev, "mobile: tap", []map[string]any{{
				"x": tapX,
				"y": tapY,
			}})
			if err == nil {
				return appiumResp, nil
			}
			dev.GetLogger().LogWarn("appium_interact",
				fmt.Sprintf("Appium tap fallback also failed for device `%s`: %v", dev.GetUDID(), err))
		}
		// Last resort：再走一次 WDA，与原始实现的兜底返回保持一致。
		return wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/tap", actionJSON, "wda/tap", actionJSON)
	} else {
		return androidRemoteServerRequestJson(dev, http.MethodPost, "tap", bytes.NewReader([]byte(actionJSON)))
	}
}

func deviceTouchAndHold(dev devices.PlatformDevice, x float64, y float64, duration float64) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		duration = float64(duration) / 1000
	}
	requestBody := struct {
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
		Duration float64 `json:"duration"`
	}{
		X:        x,
		Y:        y,
		Duration: duration,
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return nil, err
	}

	if dev.GetOS() == "ios" {
		// Fast path：WDA sessionless touchAndHold；像素坐标无需换算。
		resp, wdaErr := wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/touchAndHold", actionJSON, "wda/touchAndHold", actionJSON)
		if wdaErr == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			return resp, nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		dev.GetLogger().LogWarn("wda_interact",
			fmt.Sprintf("WDA touchAndHold fast path failed for device `%s`, falling back to Appium: %v", dev.GetUDID(), wdaErr))

		// Fallback：Appium mobile:touchAndHold（更宽容但延迟更高）。
		if hasExternalAppiumSession(dev) || dev.GetIsAppiumUp() {
			holdX, holdY, err := normalizeIOSPointForAppium(dev, x, y)
			if err != nil {
				return nil, err
			}
			appiumResp, err := executeIOSScriptViaBestSession(dev, "mobile: touchAndHold", []map[string]any{{
				"x":        holdX,
				"y":        holdY,
				"duration": duration,
			}})
			if err == nil {
				return appiumResp, nil
			}
			dev.GetLogger().LogWarn("appium_interact",
				fmt.Sprintf("Appium touchAndHold fallback also failed for device `%s`: %v", dev.GetUDID(), err))
		}
		// Last resort：再走一次 WDA。
		return wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/touchAndHold", actionJSON, "wda/touchAndHold", actionJSON)
	} else {
		return androidRemoteServerRequestJson(dev, http.MethodPost, "touchAndHold", bytes.NewReader([]byte(actionJSON)))
	}
}

func deviceScreenshot(dev devices.PlatformDevice) (string, error) {
	if dev.GetOS() == "android" {
		cmd := exec.Command("adb", "-s", dev.GetUDID(), "exec-out", "screencap", "-p")
		var out bytes.Buffer
		cmd.Stdout = &out

		err := cmd.Run()
		if err != nil {
			return "", err
		}

		// Encode PNG bytes to Base64
		base64Screenshot := base64.StdEncoding.EncodeToString(out.Bytes())

		return base64Screenshot, nil
	} else {
		iosDev, ok := dev.(*devices.IOSDevice)
		if !ok {
			return "", fmt.Errorf("device %s is not an iOS device", dev.GetUDID())
		}
		screenshotService, err := instruments.NewScreenshotService(iosDev.GoIOSDeviceEntry)
		if err != nil {
			return "", err
		}
		imageBytes, err := screenshotService.TakeScreenshot()
		if err != nil {
			return "", err
		}

		base64Screenshot := base64.StdEncoding.EncodeToString(imageBytes)

		return base64Screenshot, nil
	}
}

func deviceSwipe(dev devices.PlatformDevice, x, y, endX, endY float64) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		// 预先准备 WDA sessionless / session swipe 的 payload，避免在快路径里重复 marshal。
		requestBody := struct {
			X     float64 `json:"startX"`
			Y     float64 `json:"startY"`
			EndX  float64 `json:"endX"`
			EndY  float64 `json:"endY"`
			Delay float64 `json:"delay"`
		}{
			X:     x,
			Y:     y,
			EndX:  endX,
			EndY:  endY,
			Delay: 0.15,
		}
		actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, err
		}
		sessionRequestBody := struct {
			FromX    float64 `json:"fromX"`
			FromY    float64 `json:"fromY"`
			ToX      float64 `json:"toX"`
			ToY      float64 `json:"toY"`
			Duration float64 `json:"duration"`
		}{
			FromX:    x,
			FromY:    y,
			ToX:      endX,
			ToY:      endY,
			Duration: 0.15,
		}
		sessionActionJSON, err := json.MarshalIndent(sessionRequestBody, "", "  ")
		if err != nil {
			return nil, err
		}

		// Fast path 1：方向明确的拖拽优先走 WDA directional swipe，开销更低。
		if direction, ok := inferDirectionalSwipe(x, y, endX, endY); ok {
			resp, err := tryWDADirectionalSwipe(dev, direction)
			if err == nil {
				return resp, nil
			}
			dev.GetLogger().LogWarn("wda_interact",
				fmt.Sprintf("Directional WDA swipe fast path failed for device `%s`, falling back to coordinate swipe: %v", dev.GetUDID(), err))
		}

		// Fast path 2：WDA sessionless 坐标 swipe；与 tap 相同的快路径。
		if resp, wdaErr := wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/swipe", actionJSON, "wda/dragfromtoforduration", sessionActionJSON); wdaErr == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			return resp, nil
		} else {
			if resp != nil {
				resp.Body.Close()
			}
			dev.GetLogger().LogWarn("wda_interact",
				fmt.Sprintf("WDA coordinate swipe fast path failed for device `%s`, falling back to Appium: %v", dev.GetUDID(), wdaErr))
		}

		// Fallback A：Appium primary session（如果存在）。
		if hasExternalAppiumSession(dev) {
			primarySessionID := dev.GetAppiumSessionID()
			resp, shouldRefresh, err := executeIOSSwipeViaAppiumSession(dev, primarySessionID, x, y, endX, endY)
			if err != nil {
				dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Primary Appium session `%s` failed for device `%s`, falling back to control session: %v", primarySessionID, dev.GetUDID(), err))
			} else if !shouldRefresh {
				return resp, nil
			} else {
				clearStalePrimaryAppiumSession(dev, primarySessionID)
				dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Primary Appium session `%s` is stale for device `%s`, falling back to control session", primarySessionID, dev.GetUDID()))
			}
		}
		// Fallback B：Appium control session。
		if dev.GetIsAppiumUp() {
			resp, err := executeIOSSwipeViaControlSession(dev, x, y, endX, endY)
			if err == nil {
				return resp, nil
			}
			dev.GetLogger().LogWarn("appium_interact", fmt.Sprintf("Control-session swipe fallback failed for device `%s`: %v", dev.GetUDID(), err))
		}
		// Last resort：再走一次 WDA，与原始实现的兜底返回保持一致。
		return wdaRequestWithSessionFallback(dev, http.MethodPost, "wda/swipe", actionJSON, "wda/dragfromtoforduration", sessionActionJSON)
	} else {
		requestBody := struct {
			X     float64 `json:"x1"`
			Y     float64 `json:"y1"`
			EndX  float64 `json:"x2"`
			EndY  float64 `json:"y2"`
			Delay float64 `json:"duration"`
		}{
			X:     x,
			Y:     y,
			EndX:  endX,
			EndY:  endY,
			Delay: 500,
		}
		actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, err
		}
		return androidRemoteServerRequestJson(dev, http.MethodPost, "swipe", bytes.NewReader([]byte(actionJSON)))
	}
}

func devicePinch(dev devices.PlatformDevice, x, y, scale float64) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		requestBody := struct {
			CenterX    float64 `json:"centerX"`
			CenterY    float64 `json:"centerY"`
			StartScale float64 `json:"startScale"`
			EndScale   float64 `json:"endScale"`
			Duration   float64 `json:"duration"`
		}{
			CenterX:    x,
			CenterY:    y,
			StartScale: 1.0,
			EndScale:   scale,
			Duration:   0.5,
		}

		actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal iOS pinch payload: %w", err)
		}

		return wdaRequest(dev, http.MethodPost, "wda/pinch", bytes.NewReader(actionJSON))
	} else {
		requestBody := struct {
			CenterX   float64 `json:"centerX"`
			CenterY   float64 `json:"centerY"`
			Scale     float64 `json:"scale"`
			Duration  int     `json:"duration"`
			Direction string  `json:"direction"`
		}{
			CenterX:   x,
			CenterY:   y,
			Scale:     scale,
			Duration:  300,
			Direction: "diagonal",
		}

		actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Android pinch payload: %w", err)
		}

		return androidRemoteServerRequestJson(dev, http.MethodPost, "pinch", bytes.NewReader(actionJSON))
	}
}

func deviceDoubleTap(dev devices.PlatformDevice, x, y float64) (*http.Response, error) {
	requestBody := struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}{
		X: x,
		Y: y,
	}
	actionJSON, err := json.MarshalIndent(requestBody, "", "  ")
	if err != nil {
		return nil, err
	}

	if dev.GetOS() == "ios" {
		return wdaRequest(dev, http.MethodPost, "wda/doubleTap", bytes.NewReader(actionJSON))
	}

	return androidRemoteServerRequestJson(dev, http.MethodPost, "doubleTap", bytes.NewReader(actionJSON))
}

func deviceHome(dev devices.PlatformDevice) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		return wdaRequest(dev, http.MethodPost, "wda/homescreen", nil)
	} else {
		return androidRemoteServerRequest(dev, http.MethodPost, "home", nil)
	}
}

func deviceRecents(dev devices.PlatformDevice) error {
	if dev.GetOS() == "ios" {
		return fmt.Errorf("App switcher not supported on iOS via WDA")
	}
	cmd := exec.CommandContext(dev.GetContext(), "adb", "-s", dev.GetUDID(), "shell", "input", "keyevent", "KEYCODE_APP_SWITCH")
	return cmd.Run()
}

func activateApp(dev devices.PlatformDevice, appIdentifier string) (*http.Response, error) {
	return activateAppWithClient(controlNetClient, dev, appIdentifier)
}

func activateAppWithClient(client *http.Client, dev devices.PlatformDevice, appIdentifier string) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		requestBody := struct {
			BundleId string `json:"bundleId"`
		}{
			BundleId: appIdentifier,
		}

		reqJson, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("appiumActivateApp: Failed to marshal request body json when activating app for device `%s` - %s", dev.GetUDID(), err)
		}

		activateAppResp, err := wdaRequestWithSessionFallbackUsingClient(client, dev, http.MethodPost, "wda/apps/activate", reqJson, "wda/apps/activate", reqJson)
		if err != nil {
			return activateAppResp, err
		}
		if err := ensureSuccessfulResponse(activateAppResp, fmt.Sprintf("activating app `%s`", appIdentifier)); err != nil {
			return activateAppResp, err
		}
		return activateAppResp, nil
	}

	return nil, fmt.Errorf("App activation available only for iOS devices")
}

func wdaClipboardRequestWithClient(client *http.Client, dev devices.PlatformDevice, requestBody []byte) (*http.Response, error) {
	return wdaRequestWithSessionFallbackUsingClient(client, dev, http.MethodPost, "wda/getPasteboard", requestBody, "wda/getPasteboard", requestBody)
}

func wdaClipboardRequestAllowingPasteAlert(client *http.Client, dev devices.PlatformDevice, requestBody []byte) (*http.Response, error) {
	stopWatcher := startIOSPasteAlertWatcher(dev)
	resp, err := wdaClipboardRequestWithClient(client, dev, requestBody)
	if stopWatcher() {
		dev.GetLogger().LogDebug("wda_interact", fmt.Sprintf("Accepted iOS paste permission alert while reading clipboard for device `%s`", dev.GetUDID()))
	}
	return resp, err
}

func marshalWDAAlertName(name string) ([]byte, error) {
	if name == "" {
		return nil, nil
	}
	payload := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func wdaAlertCommandWithClient(client *http.Client, dev devices.PlatformDevice, endpoint string, name string) error {
	requestBody, err := marshalWDAAlertName(name)
	if err != nil {
		return err
	}
	action := fmt.Sprintf("calling WDA %s", endpoint)
	if name != "" {
		action = fmt.Sprintf("calling WDA %s for button `%s`", endpoint, name)
	}

	var lastErr error
	resp, err := wdaRequestWithClient(client, dev, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err == nil {
		if ensureErr := ensureSuccessfulResponse(resp, action); ensureErr == nil {
			resp.Body.Close()
			return nil
		} else {
			lastErr = ensureErr
		}
	} else {
		lastErr = err
	}

	resp, err = wdaSessionRequestWithRetryUsingClient(client, dev, http.MethodPost, endpoint, requestBody)
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%v; session fallback failed: %w", lastErr, err)
		}
		return err
	}
	if ensureErr := ensureSuccessfulResponse(resp, action); ensureErr != nil {
		if lastErr != nil {
			return fmt.Errorf("%v; session fallback failed: %w", lastErr, ensureErr)
		}
		return ensureErr
	}
	resp.Body.Close()
	return nil
}

func acceptWDAAlertWithClient(client *http.Client, dev devices.PlatformDevice, name string) error {
	return wdaAlertCommandWithClient(client, dev, "alert/accept", name)
}

func wdaAlertTextWithClient(client *http.Client, dev devices.PlatformDevice) (string, error) {
	var lastErr error
	for _, useSession := range []bool{false, true} {
		var resp *http.Response
		var err error
		if useSession {
			resp, err = wdaSessionRequestWithRetryUsingClient(client, dev, http.MethodGet, "alert/text", nil)
		} else {
			resp, err = wdaRequestWithClient(client, dev, http.MethodGet, "alert/text", nil)
		}
		if err != nil {
			lastErr = err
			continue
		}

		text, err := readWDAStringValue(resp, "getting alert text")
		if err == nil {
			return text, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("getting alert text failed")
	}
	return "", lastErr
}

func readWDAStringValue(resp *http.Response, action string) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("%s failed: empty response", action)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("%s failed with status %d: %s", action, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var valueResp struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &valueResp); err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(valueResp.Value, &value); err != nil {
		return "", fmt.Errorf("%s returned non-string value: %w", action, err)
	}
	return strings.TrimSpace(value), nil
}

func isNoSuchAlertError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such alert") ||
		strings.Contains(message, "modal dialog when one was not open")
}

func iosPasteAlertAllowButtonLabels() []string {
	return []string{"Allow Paste", "Paste", "Allow", "允许粘贴", "允许复制", "允许", "允許貼上", "貼上", "允許"}
}

func acceptIOSPasteAlertWithClient(client *http.Client, dev devices.PlatformDevice) error {
	alertText, textErr := wdaAlertTextWithClient(client, dev)
	if textErr == nil {
		if !isPastePermissionAlertMessage(alertText) {
			return fmt.Errorf("active alert is not an iOS paste permission alert: %q", alertText)
		}
	} else if isNoSuchAlertError(textErr) {
		return textErr
	}

	var lastErr error
	if textErr != nil {
		lastErr = textErr
	}
	for _, label := range iosPasteAlertAllowButtonLabels() {
		if err := acceptWDAAlertWithClient(client, dev, label); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if isNoSuchAlertError(textErr) {
		return lastErr
	}
	if err := acceptWDAAlertWithClient(client, dev, ""); err == nil {
		return nil
	} else {
		lastErr = err
	}
	return lastErr
}

func waitForIOSPasteAlertAcceptance(client *http.Client, dev devices.PlatformDevice, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := acceptIOSPasteAlertWithClient(client, dev); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if timeout <= 0 || time.Now().Add(iosPasteAlertPollInterval).After(deadline) {
			break
		}
		time.Sleep(iosPasteAlertPollInterval)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("iOS paste permission alert was not accepted before timeout")
	}
	return lastErr
}

func waitForIOSPasteAlertBestEffort(client *http.Client, dev devices.PlatformDevice, timeout time.Duration) {
	if err := waitForIOSPasteAlertAcceptance(client, dev, timeout); err != nil {
		dev.GetLogger().LogDebug("wda_interact", fmt.Sprintf("No iOS paste permission alert accepted for device `%s`: %v", dev.GetUDID(), err))
	}
}

func startIOSPasteAlertWatcher(dev devices.PlatformDevice) func() bool {
	ctx, cancel := context.WithCancel(context.Background())
	accepted := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		client := &http.Client{Timeout: iosPasteAlertRequestTimeout}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if err := acceptIOSPasteAlertWithClient(client, dev); err == nil {
				select {
				case accepted <- struct{}{}:
				default:
				}
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(iosPasteAlertPollInterval):
			}
		}
	}()

	return func() bool {
		cancel()
		select {
		case <-done:
		default:
		}
		select {
		case <-accepted:
			return true
		default:
			return false
		}
	}
}

func acceptIOSPasteAlertBestEffort(client *http.Client, dev devices.PlatformDevice) {
	if err := acceptIOSPasteAlertWithClient(client, dev); err != nil {
		dev.GetLogger().LogDebug("wda_interact", fmt.Sprintf("No iOS paste permission alert accepted for device `%s`: %v", dev.GetUDID(), err))
	}
}

func iosActiveBundleIDWithClient(client *http.Client, dev devices.PlatformDevice) (string, error) {
	resp, err := wdaRequestWithClient(client, dev, http.MethodGet, "wda/activeAppInfo", nil)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("active app response is empty")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("active app request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var activeApp struct {
		Value struct {
			BundleID string `json:"bundleId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &activeApp); err != nil {
		return "", err
	}
	return strings.TrimSpace(activeApp.Value.BundleID), nil
}

func shouldRestoreIOSForegroundBundle(bundleID string) bool {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return false
	}
	if strings.EqualFold(bundleID, config.ProviderConfig.WdaBundleID) {
		return false
	}
	return !strings.Contains(strings.ToLower(bundleID), "xctrunner") &&
		!strings.Contains(strings.ToLower(bundleID), "webdriveragent")
}

func restoreIOSForegroundAsync(dev devices.PlatformDevice, bundleID string) {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return
	}

	go func() {
		restoreClient := &http.Client{Timeout: 3 * time.Second}
		resp, err := activateAppWithClient(restoreClient, dev, bundleID)
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		if err != nil {
			dev.GetLogger().LogWarn("wda_interact", fmt.Sprintf("Failed to restore foreground app `%s` after iOS clipboard fallback for device `%s`: %v", bundleID, dev.GetUDID(), err))
		}
	}()
}

func isPastePermissionAlertMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "would like to paste") ||
		strings.Contains(message, "allow paste") ||
		strings.Contains(message, "paste from") ||
		strings.Contains(message, "允许粘贴") ||
		strings.Contains(message, "允许复制") ||
		strings.Contains(message, "想从") && strings.Contains(message, "粘贴") ||
		strings.Contains(message, "允許貼上") ||
		strings.Contains(message, "想從") && strings.Contains(message, "貼上")
}

func isWDAStallError(err error) bool {
	if err == nil {
		return false
	}
	if isPastePermissionAlertMessage(err.Error()) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "client.timeout") ||
		strings.Contains(message, "timeout awaiting response headers") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "eof")
}

func deviceGetClipboard(dev devices.PlatformDevice) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		requestBody := struct {
			ContentType string `json:"contentType"`
		}{
			ContentType: "plaintext",
		}
		reqJson, err := json.MarshalIndent(requestBody, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("appiumGetClipboard: Failed to marshal request body json when getting clipboard for device `%s` - %s", dev.GetUDID(), err)
		}

		directClient := &http.Client{Timeout: iosClipboardDirectTimeout}
		clipboardResp, err := wdaClipboardRequestAllowingPasteAlert(directClient, dev, reqJson)
		if err == nil {
			checkedResp, checkErr := successfulClipboardResponse(clipboardResp, true)
			if checkErr == nil {
				return checkedResp, nil
			}
			if checkedResp != nil {
				checkedResp.Body.Close()
			}
			err = checkErr
		}
		if err != nil {
			if isWDAStallError(err) {
				dev.GetLogger().LogWarn("wda_interact", fmt.Sprintf("Direct iOS clipboard read hit WDA/client stall for device `%s`, retrying with foreground WDA Runner fallback: %v", dev.GetUDID(), err))
			} else {
				dev.GetLogger().LogWarn("wda_interact", fmt.Sprintf("Direct iOS clipboard read missed for device `%s`, retrying with foreground WDA Runner fallback: %v", dev.GetUDID(), err))
			}
		}

		fallbackClient := &http.Client{Timeout: iosClipboardFallbackTimeout}
		restoreBundleID := ""
		if activeBundleID, activeErr := iosActiveBundleIDWithClient(directClient, dev); activeErr == nil {
			if shouldRestoreIOSForegroundBundle(activeBundleID) {
				restoreBundleID = activeBundleID
			}
		} else {
			dev.GetLogger().LogDebug("wda_interact", fmt.Sprintf("Failed to record foreground app before iOS clipboard fallback for device `%s`: %v", dev.GetUDID(), activeErr))
		}

		activateAppResp, err := activateAppWithClient(fallbackClient, dev, config.ProviderConfig.WdaBundleID)
		if activateAppResp != nil && activateAppResp.Body != nil {
			activateAppResp.Body.Close()
		}
		if err != nil {
			if restoreBundleID != "" {
				restoreIOSForegroundAsync(dev, restoreBundleID)
			}
			return activateAppResp, fmt.Errorf("appiumGetClipboard: Failed to activate WDA Runner for device `%s` clipboard fallback - %s", dev.GetUDID(), err)
		}
		if restoreBundleID != "" {
			defer restoreIOSForegroundAsync(dev, restoreBundleID)
		}

		clipboardResp, err = wdaClipboardRequestAllowingPasteAlert(fallbackClient, dev, reqJson)
		if err != nil {
			waitForIOSPasteAlertBestEffort(fallbackClient, dev, iosPasteAlertAcceptTimeout)
			time.Sleep(300 * time.Millisecond)
			clipboardResp, err = wdaClipboardRequestAllowingPasteAlert(fallbackClient, dev, reqJson)
			if err != nil {
				return clipboardResp, fmt.Errorf("appiumGetClipboard: Failed to execute WDA pasteboard request for device `%s` after foregrounding WDA Runner - %s", dev.GetUDID(), err)
			}
		}

		checkedResp, checkErr := successfulClipboardResponse(clipboardResp, true)
		if checkErr != nil {
			if checkedResp != nil {
				checkedResp.Body.Close()
			}
			waitForIOSPasteAlertBestEffort(fallbackClient, dev, iosPasteAlertAcceptTimeout)
			time.Sleep(300 * time.Millisecond)
			clipboardResp, err = wdaClipboardRequestAllowingPasteAlert(fallbackClient, dev, reqJson)
			if err != nil {
				return clipboardResp, fmt.Errorf("appiumGetClipboard: Failed to execute WDA pasteboard request for device `%s` after foreground paste alert fallback - %s", dev.GetUDID(), err)
			}
			checkedResp, checkErr = successfulClipboardResponse(clipboardResp, true)
			if checkErr != nil {
				if checkedResp != nil {
					checkedResp.Body.Close()
				}
				return checkedResp, fmt.Errorf("appiumGetClipboard: Failed to get non-empty clipboard value for device `%s` after foregrounding WDA Runner - %s", dev.GetUDID(), checkErr)
			}
		}

		return checkedResp, nil
	} else {
		return androidRemoteServerRequest(dev, http.MethodPost, "clipboard", nil)
	}
}

func executeTypeText(dev devices.PlatformDevice, text string) (*http.Response, error) {
	if dev.GetOS() == "ios" {
		// 走 per-UDID coalescer：把同时间窗内并发到达的字符合并成 1-2 次
		// wda/keys 调用，避免 hub-ui 远程键盘逐字符 POST 时被 IPC 延迟串行化。
		// 详见 provider/router/typing_coalescer.go。
		return coalescedTypeTextIOS(dev, text)
	} else {
		typeTextPayload := models.AppiumTypeText{
			Text: text,
		}
		typeJSON, err := json.MarshalIndent(typeTextPayload, "", "  ")
		if err != nil {
			return nil, err
		}
		andDev, ok := dev.(*devices.AndroidDevice)
		if !ok {
			return nil, fmt.Errorf("device %s is not an Android device", dev.GetUDID())
		}
		req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:%v/type", andDev.GetAndroidIMEPort()), bytes.NewBuffer(typeJSON))
		if err != nil {
			return nil, err
		}
		return netClient.Do(req)
	}
}

func getCenterCoordinates(dev devices.PlatformDevice) (float64, float64, error) {
	device := dev.GetDBDevice()
	width, err := strconv.ParseFloat(device.ScreenWidth, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid screen width %q: %w", device.ScreenWidth, err)
	}

	height, err := strconv.ParseFloat(device.ScreenHeight, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid screen height %q: %w", device.ScreenHeight, err)
	}

	return width / 2, height / 2, nil
}

func getIOSScreenSize(dev devices.PlatformDevice) (float64, float64, error) {
	device := dev.GetDBDevice()
	width, err := strconv.ParseFloat(device.ScreenWidth, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid screen width %q: %w", device.ScreenWidth, err)
	}

	height, err := strconv.ParseFloat(device.ScreenHeight, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid screen height %q: %w", device.ScreenHeight, err)
	}

	return width, height, nil
}

func normalizeIOSPointForAppium(dev devices.PlatformDevice, x, y float64) (float64, float64, error) {
	width, height, err := getIOSScreenSize(dev)
	if err != nil {
		return 0, 0, err
	}

	if x >= 0 && x <= 1 {
		x *= width
	}
	if y >= 0 && y <= 1 {
		y *= height
	}

	return x, y, nil
}

func normalizeCoordinates(dev devices.PlatformDevice, x, y float64) (float64, float64, error) {
	if x == 0 && y == 0 {
		return getCenterCoordinates(dev)
	}
	return x, y, nil
}

func executeCustomAction(dev devices.PlatformDevice, actionType string, params map[string]any) (*http.Response, error) {
	if params == nil {
		params = make(map[string]any)
	}

	switch actionType {
	case "tap":
		x := utils.GetFloat(params, "x", 0)
		y := utils.GetFloat(params, "y", 0)
		x, y, err := normalizeCoordinates(dev, x, y)
		if err != nil {
			return nil, fmt.Errorf("normalizing coordinates: %w", err)
		}
		return deviceTap(dev, x, y)

	case "double_tap":
		x := utils.GetFloat(params, "x", 0)
		y := utils.GetFloat(params, "y", 0)
		x, y, err := normalizeCoordinates(dev, x, y)
		if err != nil {
			return nil, fmt.Errorf("normalizing coordinates: %w", err)
		}
		return deviceDoubleTap(dev, x, y)

	case "swipe":
		x := utils.GetFloat(params, "x", 0)
		y := utils.GetFloat(params, "y", 0)
		endX := utils.GetFloat(params, "endX", 0)
		endY := utils.GetFloat(params, "endY", 0)
		return deviceSwipe(dev, x, y, endX, endY)

	case "touch_and_hold":
		x := utils.GetFloat(params, "x", 0)
		y := utils.GetFloat(params, "y", 0)
		x, y, err := normalizeCoordinates(dev, x, y)
		if err != nil {
			return nil, fmt.Errorf("normalizing coordinates: %w", err)
		}
		duration := utils.GetFloat(params, "duration", 1000)
		return deviceTouchAndHold(dev, x, y, duration)

	case "pinch":
		x := utils.GetFloat(params, "x", 0)
		y := utils.GetFloat(params, "y", 0)
		x, y, err := normalizeCoordinates(dev, x, y)
		if err != nil {
			return nil, fmt.Errorf("normalizing coordinates: %w", err)
		}
		scale := utils.GetFloat(params, "scale", 1.0)
		return devicePinch(dev, x, y, scale)

	case "type_text":
		text := utils.GetString(params, "text", "")
		return executeTypeText(dev, text)

	case "home":
		return deviceHome(dev)

	case "lock":
		return deviceLock(dev, "lock")

	case "unlock":
		return deviceLock(dev, "unlock")

	case "pinch_in":
		x := utils.GetFloat(params, "x", 250)
		y := utils.GetFloat(params, "y", 500)
		return devicePinch(dev, x, y, 0.5)

	case "pinch_out":
		x := utils.GetFloat(params, "x", 250)
		y := utils.GetFloat(params, "y", 500)
		return devicePinch(dev, x, y, 2.0)

	default:
		return nil, fmt.Errorf("unsupported action type: %s", actionType)
	}
}
