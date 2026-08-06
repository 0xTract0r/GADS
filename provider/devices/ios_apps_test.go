package devices

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"GADS/common/constants"

	"github.com/danielpaulus/go-ios/ios/installationproxy"
)

func TestMergeIOSBrowseResponseIgnoresAbnormalCurrentAmount(t *testing.T) {
	app := installationproxy.AppInfo{
		installationproxy.CFBundleIdentifier: "com.example.app",
		installationproxy.CFBundleExecutable: "Example",
	}
	got, err := mergeIOSBrowseResponse(nil, installationproxy.BrowseResponse{
		CurrentIndex:  0,
		CurrentAmount: math.MaxUint64,
		CurrentList:   []installationproxy.AppInfo{app},
		Status:        "Complete",
	}, 100)
	if err != nil {
		t.Fatalf("mergeIOSBrowseResponse returned error: %v", err)
	}
	if len(got) != 1 || got[0].CFBundleIdentifier() != "com.example.app" {
		t.Fatalf("merged apps = %#v, want the one actual CurrentList entry", got)
	}
}

func TestMergeIOSBrowseResponseRejectsSparseOrOversizedRanges(t *testing.T) {
	app := installationproxy.AppInfo{installationproxy.CFBundleIdentifier: "com.example.app"}
	if _, err := mergeIOSBrowseResponse(nil, installationproxy.BrowseResponse{
		CurrentIndex: 1,
		CurrentList:  []installationproxy.AppInfo{app},
	}, 100); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("sparse response error = %v, want gap rejection", err)
	}
	if _, err := mergeIOSBrowseResponse(nil, installationproxy.BrowseResponse{
		CurrentList: make([]installationproxy.AppInfo, 101),
	}, 100); err == nil || !strings.Contains(err.Error(), "exceeds app limit") {
		t.Fatalf("oversized response error = %v, want app-limit rejection", err)
	}
}

func TestReadBoundedIOSPlistMessageRejectsLengthBeforeAllocation(t *testing.T) {
	var encoded bytes.Buffer
	if err := binary.Write(&encoded, binary.BigEndian, uint32(1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedIOSPlistMessage(&encoded, 128); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("readBoundedIOSPlistMessage error = %v, want payload limit rejection", err)
	}
}

func TestDeviceAppsFromIOSBrowseClassifiesUserAndSystemApps(t *testing.T) {
	allApps := []installationproxy.AppInfo{
		{
			installationproxy.ApplicationType:    "User",
			installationproxy.CFBundleIdentifier: "com.example.user",
			installationproxy.CFBundleExecutable: "UserApp",
		},
		{
			installationproxy.ApplicationType:    "User",
			installationproxy.CFBundleIdentifier: "com.example.wda",
			installationproxy.CFBundleExecutable: "WebDriverAgentRunner-Runner",
		},
		{
			installationproxy.ApplicationType:    "System",
			installationproxy.CFBundleIdentifier: "com.apple.mobilesafari",
			installationproxy.CFBundleExecutable: "MobileSafari",
		},
	}

	got := deviceAppsFromIOSBrowse(allApps)
	if len(got) != 1+len(constants.IOSSystemAppsBundleIds) {
		t.Fatalf("returned app count = %d, want %d", len(got), 1+len(constants.IOSSystemAppsBundleIds))
	}
	if got[0].BundleIdentifier != "com.example.user" || !got[0].CanUninstall {
		t.Fatalf("first app = %#v, want uninstallable user app", got[0])
	}
	for _, app := range got {
		if app.BundleIdentifier == "com.apple.mobilesafari" && app.AppName != "MobileSafari" {
			t.Fatalf("Safari app name = %q, want MobileSafari", app.AppName)
		}
		if app.BundleIdentifier == "com.example.wda" {
			t.Fatal("WebDriverAgent should be excluded from user apps")
		}
	}
}
