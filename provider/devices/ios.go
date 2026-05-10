/*
 * This file is part of GADS.
 *
 * Copyright (c) 2022-2025 Nikola Shabanov
 *
 * This source code is licensed under the GNU Affero General Public License v3.0.
 * You may obtain a copy of the license at https://www.gnu.org/licenses/agpl-3.0.html
 */

package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"GADS/common"
	"GADS/common/constants"
	"GADS/common/db"
	"GADS/common/models"
	"GADS/provider/config"
	"GADS/provider/logger"
	"GADS/provider/providerutil"

	"github.com/Masterminds/semver"
	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/forward"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/instruments"
	"github.com/danielpaulus/go-ios/ios/testmanagerd"
	"github.com/danielpaulus/go-ios/ios/tunnel"
	"github.com/danielpaulus/go-ios/ios/zipconduit"
	"golang.org/x/sync/errgroup"
)

// IOSDevice holds iOS-specific runtime state alongside the shared RuntimeState.
type IOSDevice struct {
	RuntimeState
	WDAPort          string          // host port for WebDriverAgent server (device port 8100)
	WDAStreamPort    string          // host port for WebDriverAgent MJPEG stream (device port 9100)
	StreamPort       string          // host port for device video stream (device port 8765)
	WDASessionID     string          // current WebDriverAgent session ID
	GoIOSDeviceEntry ios.DeviceEntry // go-ios library device entry for USB communication
	GoIOSTunnel      tunnel.Tunnel   // userspace tunnel for iOS 17.4+
	WdaReadyChan     chan bool       // signals WebDriverAgent is up after start
}

var wdaHTTPClient = &http.Client{Timeout: 5 * time.Second}

// Port accessors for router access via type assertion.
func (d *IOSDevice) GetStreamPort() string    { return d.StreamPort }
func (d *IOSDevice) GetWDAPort() string       { return d.WDAPort }
func (d *IOSDevice) GetWDAStreamPort() string { return d.WDAStreamPort }
func (d *IOSDevice) GetWDASessionID() string  { return d.WDASessionID }

// Setup runs the full iOS device provisioning sequence.
func (d *IOSDevice) Setup() error {
	d.SetupMutex.Lock()
	defer d.SetupMutex.Unlock()

	d.SetProviderState("preparing")
	logger.ProviderLogger.LogInfo("ios_device_setup", fmt.Sprintf("Running setup for device `%v`", d.GetUDID()))

	if err := d.initGoIOSDevice(); err != nil {
		return d.resetWithError("get go-ios DeviceEntry", err)
	}
	if err := d.pair(); err != nil {
		return d.resetWithError("pair device", err)
	}
	if err := d.checkDeveloperMode(); err != nil {
		return d.resetWithError("check developer mode status", err)
	}
	if err := d.mountDeveloperImage(); err != nil {
		return d.resetWithError("mount Developer Disk Image (DDI)", err)
	}
	if err := d.getDeviceInfoAndScreenSize(); err != nil {
		return err // already reset inside
	}
	if err := d.setupTunnelIfNeeded(); err != nil {
		return err // already reset inside
	}

	time.Sleep(1 * time.Second)
	d.disableBroadcastExtensionMemoryLimit()

	if err := d.allocateAndForwardPorts(); err != nil {
		return d.resetWithError("allocate or forward ports", err)
	}
	if err := d.startWebDriverAgent(); err != nil {
		return err // already reset inside
	}
	if err := d.waitForWebDriverAgent(); err != nil {
		return err // already reset inside
	}
	if err := d.applyStreamConfig(); err != nil {
		return d.resetWithError("apply device stream settings", err)
	}
	if err := d.setupAppiumIfNeeded(); err != nil {
		return err
	}

	d.InstalledApps = d.GetInstalledAppBundleIDs()
	d.SetProviderState("live")
	return nil
}

func (d *IOSDevice) initGoIOSDevice() error {
	goIosDeviceEntry, err := ios.GetDevice(d.GetUDID())
	if err != nil {
		return fmt.Errorf("could not get go-ios DeviceEntry for device `%s` - %w", d.GetUDID(), err)
	}
	d.GoIOSDeviceEntry = goIosDeviceEntry
	return nil
}

func (d *IOSDevice) checkDeveloperMode() error {
	if d.SemVer.Major() < 16 {
		return nil
	}
	devModeEnabled, err := imagemounter.IsDevModeEnabled(d.GoIOSDeviceEntry)
	if err != nil {
		return fmt.Errorf("could not check developer mode status on device `%s` - %w", d.GetUDID(), err)
	}
	if !devModeEnabled {
		return fmt.Errorf("device `%s` is iOS 16+ but developer mode is not enabled", d.GetUDID())
	}
	return nil
}

func (d *IOSDevice) getDeviceInfoAndScreenSize() error {
	plistValues, err := ios.GetValuesPlist(d.GoIOSDeviceEntry)
	if err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Could not get info plist values with go-ios for device `%v` - %v", d.GetUDID(), err))
		d.Reset("Failed to get info plist values with go-ios.")
		return err
	}
	d.HardwareModel = plistValues["HardwareModel"].(string)

	if d.DBDevice.ScreenHeight == "" || d.DBDevice.ScreenWidth == "" {
		if err := d.updateScreenSize(plistValues["ProductType"].(string)); err != nil {
			logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to update screen dimensions for device `%s` - %s", d.GetUDID(), err))
			d.Reset("Failed to update screen dimensions for device.")
			return err
		}
	}
	return nil
}

func (d *IOSDevice) setupTunnelIfNeeded() error {
	tunnelPort, err := providerutil.GetFreePort()
	if err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Could not allocate free tunnel port for device `%v` - %v", d.GetUDID(), err))
		d.Reset("Failed to allocate free tunnel port for device.")
		return err
	}
	intTunnelPort, _ := strconv.Atoi(tunnelPort)
	d.GoIOSDeviceEntry.UserspaceTUNPort = intTunnelPort

	if d.SemVer.Compare(semver.MustParse("17.4.0")) < 0 {
		return nil
	}

	deviceTunnel, err := d.createTunnel()
	if err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to create userspace tunnel for device `%s` - %v", d.GetUDID(), err))
		d.Reset("Failed to create userspace tunnel for device.")
		return err
	}
	d.GoIOSTunnel = deviceTunnel

	d.GoIOSDeviceEntry.UserspaceTUNPort = d.GoIOSTunnel.UserspaceTUNPort
	d.GoIOSDeviceEntry.UserspaceTUN = d.GoIOSTunnel.UserspaceTUN

	if err := d.deviceWithRsdProvider(); err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to create go-ios device entry with rsd provider for device `%s` - %v", d.GetUDID(), err))
		d.Reset("Failed to create go-ios device entry with rsd provider for device.")
		return err
	}
	return nil
}

func (d *IOSDevice) disableBroadcastExtensionMemoryLimit() {
	if d.DBDevice.StreamType != models.IOSWebRTCBroadcastExtensionId {
		return
	}
	pid, err := d.getProcessPid("gads-broadcast-extension")
	if err != nil {
		logger.ProviderLogger.LogWarn("ios_device_setup", fmt.Sprintf("GADS broadcast extension process is not running on device `%s`; continuing device setup without disabling its memory limit - %s", d.GetUDID(), err))
		return
	}
	if err := d.disableProcessMemoryLimit(pid); err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to disable memory limit for GADS broadcast extension process on device `%s` - %s", d.GetUDID(), err))
	}
}

func (d *IOSDevice) allocateAndForwardPorts() error {
	wdaPort, err := providerutil.GetFreePort()
	if err != nil {
		return fmt.Errorf("could not allocate free WebDriverAgent port - %w", err)
	}
	d.WDAPort = wdaPort

	streamPort, err := providerutil.GetFreePort()
	if err != nil {
		return fmt.Errorf("could not allocate free iOS stream port - %w", err)
	}
	d.StreamPort = streamPort

	wdaStreamPort, err := providerutil.GetFreePort()
	if err != nil {
		return fmt.Errorf("could not allocate free WebDriverAgent stream port - %w", err)
	}
	d.WDAStreamPort = wdaStreamPort

	go d.goIosForward(d.WDAPort, "8100")
	go d.goIosForward(d.StreamPort, "8765")
	go d.goIosForward(d.WDAStreamPort, "9100")
	return nil
}

func (d *IOSDevice) startWebDriverAgent() error {
	if d.SemVer.Major() < 17 || d.SemVer.Compare(semver.MustParse("17.4.0")) >= 0 {
		if err := d.installApp(fmt.Sprintf("%s/WebDriverAgent.ipa", config.ProviderConfig.ProviderFolder)); err != nil {
			logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Could not install WebDriverAgent on device `%s` - %s", d.GetUDID(), err))
			d.Reset("Failed to install WebDriverAgent on device.")
			return err
		}
		go d.runWDA()
	} else {
		if err := d.launchApp(config.ProviderConfig.WdaBundleID, true); err != nil {
			logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Could not launch WebDriverAgent on device `%s` - %s", d.GetUDID(), err))
			d.Reset("Failed to launch WebDriverAgent on device.")
			return err
		}
	}
	return nil
}

func (d *IOSDevice) waitForWebDriverAgent() error {
	go d.checkWebDriverAgentUp()

	select {
	case <-d.WdaReadyChan:
		logger.ProviderLogger.LogInfo("ios_device_setup", fmt.Sprintf("Successfully started WebDriverAgent for device `%v` forwarded on port %v", d.GetUDID(), d.WDAPort))
		return nil
	case <-time.After(60 * time.Second):
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Did not successfully start WebDriverAgent on device `%v` in 60 seconds", d.GetUDID()))
		d.Reset("Failed to start WebDriverAgent on device.")
		return fmt.Errorf("WDA did not start in time")
	}
}

func (d *IOSDevice) applyStreamConfig() error {
	if err := d.ApplyStreamSettings(); err != nil {
		return fmt.Errorf("could not apply device stream settings - %w", err)
	}
	if err := d.UpdateStreamSettingsOnDevice(); err != nil {
		return fmt.Errorf("could not create WebDriverAgent session or update its stream settings - %w", err)
	}
	return nil
}

func (d *IOSDevice) setupAppiumIfNeeded() error {
	if !config.ProviderConfig.SetupAppiumServers {
		return nil
	}
	return setupAppiumForDevice(d)
}

// Reset overrides RuntimeState.Reset to close iOS tunnels and free iOS-specific ports.
func (d *IOSDevice) Reset(reason string) {
	if d.ResetBase(reason) {
		d.WDASessionID = ""
		if d.GoIOSTunnel.Address != "" {
			d.GoIOSTunnel.Close()
		}
		common.MutexManager.LocalDevicePorts.Lock()
		delete(providerutil.UsedPorts, d.WDAPort)
		delete(providerutil.UsedPorts, d.StreamPort)
		delete(providerutil.UsedPorts, d.WDAStreamPort)
		common.MutexManager.LocalDevicePorts.Unlock()
	}
}

func isInvalidWDASessionResponse(statusCode int, body []byte) bool {
	if statusCode < http.StatusBadRequest {
		return false
	}
	responseText := strings.ToLower(string(body))
	return strings.Contains(responseText, "invalid session id") ||
		strings.Contains(responseText, "session does not exist") ||
		strings.Contains(responseText, "session not created")
}

// AppiumCapabilities returns the iOS-specific Appium server capabilities.
func (d *IOSDevice) AppiumCapabilities() models.AppiumServerCapabilities {
	return models.AppiumServerCapabilities{
		UDID:                  d.GetUDID(),
		WdaURL:                "http://localhost:" + d.WDAPort,
		WdaLocalPort:          d.WDAPort,
		WdaLaunchTimeout:      "120000",
		WdaConnectionTimeout:  "240000",
		ClearSystemFiles:      "false",
		PreventWdaAttachments: "true",
		SimpleIsVisibleCheck:  "false",
		AutomationName:        "XCUITest",
		PlatformName:          "iOS",
		DeviceName:            d.DBDevice.Name,
	}
}

func (d *IOSDevice) goIosForward(hostPort string, devicePort string) {
	hostPortInt, _ := strconv.Atoi(hostPort)
	devicePortInt, _ := strconv.Atoi(devicePort)
	isVideoStreamPort := devicePort == "8765" || devicePort == "9100"

	for {
		cl, err := forward.Forward(d.GoIOSDeviceEntry, uint16(hostPortInt), uint16(devicePortInt))
		if err != nil {
			if isVideoStreamPort {
				logger.ProviderLogger.LogWarn("ios_device_setup", fmt.Sprintf("Failed to forward video stream port %s to host port %s for iOS device `%s`; keeping device live and retrying - %s", devicePort, hostPort, d.GetUDID(), err))
				select {
				case <-d.Context.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}
			logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to forward device port %s to host port %s for device `%s` - %s", devicePort, hostPort, d.GetUDID(), err))
			d.Reset("Failed to forward device port to host port due to an error.")
			return
		}

		<-d.Context.Done()
		cl.Close()
		return
	}
}

func (d *IOSDevice) readWDASessionIDFromStatus() (string, error) {
	statusURL := fmt.Sprintf("http://localhost:%v/status", d.WDAPort)
	statusResp, err := wdaHTTPClient.Get(statusURL)
	if err != nil {
		return "", err
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(statusResp.Body)
		return "", fmt.Errorf("WDA status failed with status %d: %s", statusResp.StatusCode, string(body))
	}

	var statusPayload struct {
		SessionID string `json:"sessionId"`
		Value     struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusPayload); err != nil {
		return "", err
	}

	sessionID := statusPayload.SessionID
	if sessionID == "" {
		sessionID = statusPayload.Value.SessionID
	}
	return sessionID, nil
}

// ClearWDASessionID drops the cached WDA session so the next request re-syncs from live status.
func (d *IOSDevice) ClearWDASessionID() {
	d.WDASessionID = ""
}

func (d *IOSDevice) resolveWDASessionID(forceRefresh bool) (string, error) {
	if forceRefresh {
		d.WDASessionID = ""
	}

	if sessionID, err := d.readWDASessionIDFromStatus(); err == nil && sessionID != "" {
		if d.WDASessionID != "" && d.WDASessionID != sessionID {
			logger.ProviderLogger.LogWarn("wda_interact", fmt.Sprintf("WDA session for device `%s` changed from `%s` to `%s`, refreshing cached session", d.GetUDID(), d.WDASessionID, sessionID))
		}
		d.WDASessionID = sessionID
		return sessionID, nil
	}

	if d.WDASessionID != "" {
		return d.WDASessionID, nil
	}

	requestBody := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{},
			"firstMatch":  []map[string]any{{}},
		},
	}
	requestJSON, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshal WDA session payload: %w", err)
	}

	createURL := fmt.Sprintf("http://localhost:%v/session", d.WDAPort)
	resp, err := wdaHTTPClient.Post(createURL, "application/json", bytes.NewReader(requestJSON))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("create WDA session failed with status %d: %s", resp.StatusCode, string(body))
	}

	var sessionResp struct {
		SessionID string `json:"sessionId"`
		Value     struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &sessionResp); err != nil {
		return "", fmt.Errorf("decode WDA session response: %w", err)
	}

	sessionID := sessionResp.SessionID
	if sessionID == "" {
		sessionID = sessionResp.Value.SessionID
	}
	if sessionID == "" {
		return "", fmt.Errorf("WDA session response missing sessionId")
	}

	d.WDASessionID = sessionID
	return sessionID, nil
}

// EnsureWDASessionID resolves the current live WDA session, preferring /status over stale cache.
func (d *IOSDevice) EnsureWDASessionID() (string, error) {
	return d.resolveWDASessionID(false)
}

// RefreshWDASessionID forces the next lookup to drop stale cache and re-sync from live WDA.
func (d *IOSDevice) RefreshWDASessionID() (string, error) {
	return d.resolveWDASessionID(true)
}

func (d *IOSDevice) postWDASettings(url string, requestBody []byte) (*http.Response, []byte, error) {
	response, err := wdaHTTPClient.Post(url, "application/json", bytes.NewReader(requestBody))
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, err
	}
	return response, body, nil
}

// UpdateStreamSettingsOnDevice updates WebDriverAgent stream settings.
func (d *IOSDevice) UpdateStreamSettingsOnDevice() error {
	requestSettings := map[string]any{
		"mjpegServerFramerate":         d.StreamTargetFPS,
		"mjpegServerScreenshotQuality": d.StreamJpegQuality,
		"mjpegScalingFactor":           d.StreamScalingFactor,
		// 这些等待会直接放大 tap/swipe 延迟，当前场景优先响应速度。
		"waitForIdleTimeout":      0,
		"animationCoolOffTimeout": 0,
	}
	requestBody, err := json.Marshal(map[string]any{"settings": requestSettings})
	if err != nil {
		return err
	}

	sessionID, err := d.EnsureWDASessionID()
	if err == nil && sessionID != "" {
		sessionURL := fmt.Sprintf("http://localhost:%v/session/%s/appium/settings", d.WDAPort, sessionID)
		response, body, err := d.postWDASettings(sessionURL, requestBody)
		if err == nil {
			if response.StatusCode == http.StatusOK {
				return nil
			}
			if isInvalidWDASessionResponse(response.StatusCode, body) {
				refreshedSessionID, refreshErr := d.RefreshWDASessionID()
				if refreshErr == nil && refreshedSessionID != "" {
					sessionURL = fmt.Sprintf("http://localhost:%v/session/%s/appium/settings", d.WDAPort, refreshedSessionID)
					response, body, err = d.postWDASettings(sessionURL, requestBody)
					if err == nil && response.StatusCode == http.StatusOK {
						return nil
					}
				}
			}
			if response.StatusCode != http.StatusNotFound {
				return fmt.Errorf("could not successfully update WDA session settings, status code=%v body=%s", response.StatusCode, string(body))
			}
		}
	}

	url := fmt.Sprintf("http://localhost:%v/appium/settings", d.WDAPort)
	response, body, err := d.postWDASettings(url, requestBody)
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusNotFound {
		logger.ProviderLogger.LogWarn("ios_device_setup", fmt.Sprintf("WebDriverAgent on device `%s` does not support /appium/settings, continuing with default stream settings", d.GetUDID()))
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("could not successfully update WDA stream settings, status code=%v body=%s", response.StatusCode, string(body))
	}
	return nil
}

func (d *IOSDevice) mountDeveloperImage() error {
	basedir := fmt.Sprintf("%s/devimages", config.ProviderConfig.ProviderFolder)

	path, err := imagemounter.DownloadImageFor(d.GoIOSDeviceEntry, basedir)
	if err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to download DDI for device `%s` to path `%s` - %s", d.GetUDID(), basedir, err))
		return fmt.Errorf("failed to download DDI: %w", err)
	}

	err = imagemounter.MountImage(d.GoIOSDeviceEntry, path)
	if err != nil {
		if strings.Contains(err.Error(), "already mounted") || strings.Contains(err.Error(), "AlreadyMounted") {
			return nil
		}
		return fmt.Errorf("failed to mount DDI: %w", err)
	}
	return nil
}

func (d *IOSDevice) pair() (pairErr error) {
	if config.ProviderConfig.UseIOSPairCache {
		if err := restorePairRecordToUsbmuxd(d.GetUDID()); err == nil {
			logger.ProviderLogger.LogInfo("ios_device_setup", fmt.Sprintf("Restored cached pairing record for device `%s`, skipping pairing", d.GetUDID()))
			return nil
		}
	}

	logger.ProviderLogger.LogInfo("ios_device_setup", fmt.Sprintf("Pairing device `%s`", d.GetUDID()))

	defer func() {
		if pairErr == nil && config.ProviderConfig.UseIOSPairCache {
			cachePairRecord(d.GetUDID())
		}
	}()

	p12, err := os.ReadFile(fmt.Sprintf("%s/supervision.p12", config.ProviderConfig.ProviderFolder))
	if err != nil {
		logger.ProviderLogger.LogWarn("ios_device_setup", fmt.Sprintf("Could not read supervision.p12 file when pairing device with UDID: %s, falling back to unsupervised pairing - %s", d.GetUDID(), err))
		err = ios.Pair(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("Could not perform unsupervised pairing successfully - %s", err)
		}
		return nil
	}

	if config.ProviderConfig.SupervisionPassword == "" {
		err = ios.Pair(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("Could not perform unsupervised pairing successfully - %s", err)
		}
		return nil
	}
	err = ios.PairSupervised(d.GoIOSDeviceEntry, p12, config.ProviderConfig.SupervisionPassword)
	if err != nil {
		logger.ProviderLogger.LogWarn("ios_device_setup", fmt.Sprintf("Failed to perform supervised pairing on device `%s`, falling back to unsupervised - %s", d.GetUDID(), err))
		err = ios.Pair(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("Could not perform unsupervised pairing successfully - %s", err)
		}
		return nil
	}
	return nil
}

func (d *IOSDevice) getAllApps() ([]installationproxy.AppInfo, error) {
	svc, err := installationproxy.New(d.GoIOSDeviceEntry)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to installation proxy for all apps: %w", err)
	}
	defer svc.Close()
	return svc.BrowseAllApps()
}

func (d *IOSDevice) getUserApps() ([]installationproxy.AppInfo, error) {
	svc, err := installationproxy.New(d.GoIOSDeviceEntry)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to installation proxy for user apps: %w", err)
	}
	defer svc.Close()
	return svc.BrowseUserApps()
}

// GetInstalledApps returns detailed info about installed apps.
func (d *IOSDevice) GetInstalledApps() ([]models.DeviceApp, error) {
	var installedApps = make([]models.DeviceApp, 0)
	var allApps, userApps []installationproxy.AppInfo

	g, _ := errgroup.WithContext(context.Background())
	g.Go(func() error {
		var err error
		allApps, err = d.getAllApps()
		return err
	})
	g.Go(func() error {
		var err error
		userApps, err = d.getUserApps()
		return err
	})
	if err := g.Wait(); err != nil {
		return installedApps, err
	}

	bundleIdToExecutable := make(map[string]string, len(allApps))
	for _, app := range allApps {
		bundleIdToExecutable[app.CFBundleIdentifier()] = app.CFBundleExecutable()
	}

	for _, userApp := range userApps {
		if !strings.Contains(userApp.CFBundleExecutable(), "WebDriverAgentRunner") && !strings.Contains(userApp.CFBundleExecutable(), "h264-broadcast-extension") {
			installedApps = append(installedApps, models.DeviceApp{AppName: userApp.CFBundleExecutable(), BundleIdentifier: userApp.CFBundleIdentifier(), CanUninstall: true})
		}
	}

	for _, bundleId := range constants.IOSSystemAppsBundleIds {
		appName := bundleIdToExecutable[bundleId]
		if appName == "" {
			appName = "Unknown name"
		}
		installedApps = append(installedApps, models.DeviceApp{AppName: appName, BundleIdentifier: bundleId, CanUninstall: false})
	}

	return installedApps, nil
}

// GetInstalledAppBundleIDs returns the bundle identifiers of all installed apps.
func (d *IOSDevice) GetInstalledAppBundleIDs() []string {
	var bundleIdentifiers = make([]string, 0)
	installedAppsInfo, err := d.GetInstalledApps()
	if err != nil {
		return bundleIdentifiers
	}
	for _, installedApp := range installedAppsInfo {
		bundleIdentifiers = append(bundleIdentifiers, installedApp.BundleIdentifier)
	}
	return bundleIdentifiers
}

// UninstallApp uninstalls an app by bundle ID.
func (d *IOSDevice) UninstallApp(bundleID string) error {
	svc, err := installationproxy.New(d.GoIOSDeviceEntry)
	if err != nil {
		return fmt.Errorf("failed creating installation proxy connection - %v", err)
	}
	return svc.Uninstall(bundleID)
}

// InstallApp installs an app from a file in the provider folder.
func (d *IOSDevice) InstallApp(appName string) error {
	appPath := fmt.Sprintf("%s/%s", config.ProviderConfig.ProviderFolder, appName)
	return d.installApp(appPath)
}

func (d *IOSDevice) installApp(appPath string) error {
	if config.ProviderConfig.OS == "windows" {
		appPath = strings.TrimPrefix(appPath, "./")
	}

	logger.ProviderLogger.LogInfo("install_app_ios", fmt.Sprintf("Attempting to install app `%s` on device `%s`", appPath, d.GetUDID()))
	conn, err := zipconduit.New(d.GoIOSDeviceEntry)
	if err != nil {
		logger.ProviderLogger.LogInfo("install_app_ios", fmt.Sprintf("Failed to create zipconduit connection when installing app `%s` on device `%s`", appPath, d.GetUDID()))
		d.Reset("Failed to create zipconduit connection for app installation.")
		return err
	}
	conn.SendFile(appPath)
	return nil
}

func (d *IOSDevice) launchApp(bundleID string, killExisting bool) error {
	pControl, err := instruments.NewProcessControl(d.GoIOSDeviceEntry)
	if err != nil {
		return fmt.Errorf("failed to initiate process control - %s", err)
	}

	opts := map[string]any{}
	if killExisting {
		opts["KillExisting"] = 1
	}
	_, err = pControl.LaunchAppWithArgs(bundleID, nil, nil, opts)
	if err != nil {
		d.Reset("Failed to launch app with bundleID due to process control error.")
		return fmt.Errorf("failed to launch app with bundleID `%s` - %s", bundleID, err)
	}
	return nil
}

// LaunchApp launches an app by bundle ID (for the PlatformDevice interface).
func (d *IOSDevice) LaunchApp(bundleID string) error {
	return d.launchApp(bundleID, true)
}

func (d *IOSDevice) checkWebDriverAgentUp() {
	var netClient = &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%v/status", d.WDAPort), nil)

	loops := 0
	for {
		select {
		case <-d.Context.Done():
			return
		default:
		}

		if loops >= 60 {
			return
		}
		resp, err := netClient.Do(req)
		if err != nil {
			time.Sleep(1 * time.Second)
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				select {
				case d.WdaReadyChan <- true:
				default:
				}
				return
			}
			time.Sleep(1 * time.Second)
		}
		loops++
	}
}

func (d *IOSDevice) createTunnel() (tunnel.Tunnel, error) {
	tun, err := tunnel.ConnectUserSpaceTunnelLockdown(d.GoIOSDeviceEntry, d.GoIOSDeviceEntry.UserspaceTUNPort)
	tun.UserspaceTUN = true
	tun.UserspaceTUNPort = d.GoIOSDeviceEntry.UserspaceTUNPort
	return tun, err
}

func (d *IOSDevice) deviceWithRsdProvider() error {
	rsdService, err := ios.NewWithAddrPortDevice(d.GoIOSTunnel.Address, d.GoIOSTunnel.RsdPort, d.GoIOSDeviceEntry)
	if err != nil {
		return err
	}
	defer rsdService.Close()
	rsdProvider, err := rsdService.Handshake()
	if err != nil {
		return err
	}
	newEntry, err := ios.GetDeviceWithAddress(d.GetUDID(), d.GoIOSTunnel.Address, rsdProvider)
	if err != nil {
		return err
	}
	newEntry.UserspaceTUN = d.GoIOSDeviceEntry.UserspaceTUN
	newEntry.UserspaceTUNPort = d.GoIOSDeviceEntry.UserspaceTUNPort
	d.GoIOSDeviceEntry = newEntry

	return nil
}

func (d *IOSDevice) runWDA() {
	testConfig := testmanagerd.TestConfig{
		BundleId:           config.ProviderConfig.WdaBundleID,
		TestRunnerBundleId: config.ProviderConfig.WdaBundleID,
		XctestConfigName:   "WebDriverAgentRunner.xctest",
		Device:             d.GoIOSDeviceEntry,
		Listener:           testmanagerd.NewTestListener(io.Discard, io.Discard, os.TempDir()),
	}
	_, err := testmanagerd.RunTestWithConfig(d.Context, testConfig)
	if err != nil {
		logger.ProviderLogger.LogError("ios_device_setup", fmt.Sprintf("Failed to run WebDriverAgent via testmanagerd on device `%s` - %s", d.GetUDID(), err))
		d.Reset("Failed to run WebDriverAgent due to an error.")
	}
}

func (d *IOSDevice) updateScreenSize(deviceMachineCode string) error {
	if dimensions, ok := constants.IOSDeviceInfoMap[deviceMachineCode]; ok {
		d.DBDevice.ScreenHeight = dimensions.Height
		d.DBDevice.ScreenWidth = dimensions.Width
	} else {
		return fmt.Errorf("could not find `%s` device machine code in the IOSDeviceInfoMap map", deviceMachineCode)
	}

	if err := db.GlobalMongoStore.AddOrUpdateDevice(&d.DBDevice); err != nil {
		return fmt.Errorf("failed to update DB with new device dimensions - %s", err)
	}
	return nil
}

func (d *IOSDevice) getProcessPid(processName string) (uint64, error) {
	svc, err := instruments.NewDeviceInfoService(d.GoIOSDeviceEntry)
	if err != nil {
		return 0, fmt.Errorf("failed to create device info service for device `%s`", d.GetUDID())
	}
	defer svc.Close()

	processList, err := svc.ProcessList()
	if err != nil {
		return 0, fmt.Errorf("failed to get process list for device `%s` - %s", d.GetUDID(), err)
	}

	for _, process := range processList {
		if process.Pid > 1 && process.Name == processName {
			return process.Pid, nil
		}
	}
	return 0, fmt.Errorf("no process with name `%s` found on device `%s`", processName, d.GetUDID())
}

func (d *IOSDevice) disableProcessMemoryLimit(pid uint64) error {
	pControl, err := instruments.NewProcessControl(d.GoIOSDeviceEntry)
	if err != nil {
		return fmt.Errorf("failed to create process control instance for device `%s` - %s", d.GetUDID(), err)
	}

	disabled, err := pControl.DisableMemoryLimit(pid)
	if err != nil {
		return fmt.Errorf("failed to disable memory limit for pid `%v` for device `%s` - %s", pid, d.GetUDID(), err)
	}
	if !disabled {
		return fmt.Errorf("failed to disable memory limit for pid `%v` for device `%s` without explicit error", pid, d.GetUDID())
	}
	return nil
}

// GetRunningApps returns a list of running apps on the device that are killable.
func (d *IOSDevice) GetRunningApps() ([]models.RunningApp, error) {
	var runningApps = make([]models.RunningApp, 0)

	var allApps, userApps []installationproxy.AppInfo
	var procList []instruments.ProcessInfo

	g, _ := errgroup.WithContext(context.Background())
	g.Go(func() error {
		svc, err := installationproxy.New(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("failed to connect to installation proxy for all apps: %w", err)
		}
		defer svc.Close()
		allApps, err = svc.BrowseAllApps()
		return err
	})
	g.Go(func() error {
		svc, err := installationproxy.New(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("failed to connect to installation proxy for user apps: %w", err)
		}
		defer svc.Close()
		userApps, err = svc.BrowseUserApps()
		return err
	})
	g.Go(func() error {
		svc, err := instruments.NewDeviceInfoService(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("failed to create device info service: %w", err)
		}
		defer svc.Close()
		procList, err = svc.ProcessList()
		return err
	})

	if err := g.Wait(); err != nil {
		return runningApps, err
	}

	execToBundleId := make(map[string]string, len(allApps))
	for _, app := range allApps {
		execToBundleId[app.CFBundleExecutable()] = app.CFBundleIdentifier()
	}

	appsAllowList := make(map[string]bool)
	for _, bundleId := range constants.IOSSystemAppsBundleIds {
		appsAllowList[bundleId] = true
	}
	for _, userApp := range userApps {
		if !strings.Contains(userApp.CFBundleExecutable(), "WebDriverAgentRunner") && !strings.Contains(userApp.CFBundleExecutable(), "h264-broadcast-extension") {
			appsAllowList[userApp.CFBundleIdentifier()] = true
		}
	}

	for _, proc := range procList {
		bundleID, found := execToBundleId[proc.Name]
		if !found {
			continue
		}
		if appsAllowList[bundleID] {
			runningApps = append(runningApps, models.RunningApp{AppName: proc.Name, BundleIdentifier: bundleID})
		}
	}

	return runningApps, nil
}

// KillApp kills a running app by bundle identifier.
func (d *IOSDevice) KillApp(bundleIdentifier string) error {
	var allApps []installationproxy.AppInfo
	var processList []instruments.ProcessInfo

	g, _ := errgroup.WithContext(context.Background())
	g.Go(func() error {
		svc, err := installationproxy.New(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("failed to connect to installation proxy: %w", err)
		}
		defer svc.Close()
		allApps, err = svc.BrowseAllApps()
		return err
	})
	g.Go(func() error {
		infoService, err := instruments.NewDeviceInfoService(d.GoIOSDeviceEntry)
		if err != nil {
			return fmt.Errorf("failed to create device info service - %w", err)
		}
		defer infoService.Close()
		processList, err = infoService.ProcessList()
		return err
	})

	if err := g.Wait(); err != nil {
		return err
	}

	pControl, err := instruments.NewProcessControl(d.GoIOSDeviceEntry)
	if err != nil {
		return fmt.Errorf("failed to create process control service - %w", err)
	}
	defer pControl.Close()

	var appProcessName string
	for _, app := range allApps {
		if app.CFBundleIdentifier() == bundleIdentifier {
			appProcessName = app.CFBundleExecutable()
		}
	}
	if appProcessName == "" {
		return fmt.Errorf("app with bundle identifier `%s` is not installed on device", bundleIdentifier)
	}

	for _, p := range processList {
		if p.Name == appProcessName {
			return pControl.KillProcess(p.Pid)
		}
	}
	return fmt.Errorf("app with bundle id `%s` is not running", bundleIdentifier)
}

// GetScreenSize returns the device screen dimensions.
func (d *IOSDevice) GetScreenSize() (width, height string, err error) {
	return d.DBDevice.ScreenWidth, d.DBDevice.ScreenHeight, nil
}

// GetHardwareModel returns the hardware model string.
func (d *IOSDevice) GetHardwareModel() (string, error) {
	return d.HardwareModel, nil
}

// GetCurrentRotation returns the current device rotation (iOS uses WDA for this, handled by router).
func (d *IOSDevice) GetCurrentRotation() (string, error) {
	return d.CurrentRotation, nil
}

// ChangeRotation is handled via WDA in the router for iOS.
func (d *IOSDevice) ChangeRotation(rotation string) error {
	return fmt.Errorf("iOS rotation is handled via WebDriverAgent")
}

// ApplyStreamSettings applies stream settings from DB to the device runtime state.
func (d *IOSDevice) ApplyStreamSettings() error {
	return applyDeviceStreamSettings(d)
}

// Gets the connected iOS devices using the `go-ios` library
func getConnectedDevicesIOS() []string {
	var connectedDevices []string

	deviceList, err := ios.ListDevices()
	if err != nil {
		return connectedDevices
	}

	for _, connDevice := range deviceList.DeviceList {
		connectedDevices = append(connectedDevices, connDevice.Properties.SerialNumber)
	}
	return connectedDevices
}
