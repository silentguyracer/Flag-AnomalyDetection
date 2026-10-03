package deviceprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyze_AntiDetectSpoofedBrowser(t *testing.T) {
	// Anti-detect bot: claims to be Windows 11 with Direct3D, but underlying GPU is Apple M2 Pro
	spoofed := &RawDeviceMetrics{
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		Platform:            "Win32",
		HardwareConcurrency: 8,
		DeviceMemoryGB:      16,
		ScreenResolution:    "1920x1080",
		ColorDepth:          24,
		WebGLRenderer:       "ANGLE (Apple, Apple M2 Pro, OpenGL 4.1)",
		WebGLVendor:         "Google Inc. (Apple)",
		CanvasHash:          "a94f83b102ce88d1",
		AudioContextHash:    "f831902ba9e1c34a",
	}

	verdict := Analyze(spoofed)
	require.True(t, verdict.IsSpoofed, "Should detect Windows UA with Apple M2 GPU as spoofed")
	assert.GreaterOrEqual(t, verdict.RiskScore, 0.85)
	assert.Contains(t, verdict.SpoofingIndicators[0], "Platform Inconsistency")
	assert.NotEmpty(t, verdict.DeviceFingerprint)
}

func TestAnalyze_HeadlessVMSoftwareRasterizer(t *testing.T) {
	// Cloud headless runner using SwiftShader software rasterizer
	vmMetrics := &RawDeviceMetrics{
		UserAgent:           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/128.0.0.0 Safari/537.36",
		Platform:            "Linux x86_64",
		HardwareConcurrency: 2,
		DeviceMemoryGB:      4,
		ScreenResolution:    "800x600",
		ColorDepth:          24,
		WebGLRenderer:       "Google SwiftShader",
		WebGLVendor:         "Google Inc.",
		CanvasHash:          "", // Blocked canvas
		AudioContextHash:    "",
	}

	verdict := Analyze(vmMetrics)
	assert.True(t, verdict.IsVirtualMachine, "Should detect Google SwiftShader as virtual machine")
	assert.True(t, verdict.IsSpoofed)
	assert.GreaterOrEqual(t, verdict.RiskScore, 0.90)
}

func TestAnalyze_GenuineDevice(t *testing.T) {
	// Genuine macOS device with matching hardware and high entropy
	genuine := &RawDeviceMetrics{
		UserAgent:           "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		Platform:            "MacIntel",
		HardwareConcurrency: 10,
		DeviceMemoryGB:      16,
		ScreenResolution:    "2560x1440",
		ColorDepth:          30,
		WebGLRenderer:       "ANGLE (Apple, Apple M1 Max, OpenGL 4.1)",
		WebGLVendor:         "Google Inc. (Apple)",
		CanvasHash:          "498abfc1200192ea",
		AudioContextHash:    "bc9910481fa73e02",
		InstalledFontsCount: 85,
	}

	verdict := Analyze(genuine)
	assert.False(t, verdict.IsSpoofed, "Genuine matching hardware should not be flagged as spoofed")
	assert.False(t, verdict.IsVirtualMachine)
	assert.Equal(t, 0.0, verdict.RiskScore)
	assert.GreaterOrEqual(t, verdict.EntropyScore, 0.90)
}
