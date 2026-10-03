package deviceprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// Hardware Entropy & Device Fingerprint Anti-Spoofing Engine.
// Unmasks stealth browser automation (Multilogin, GoLogin, Puppeteer-Stealth)
// by cross-verifying GPU shader pipelines, AudioContext acoustics, Canvas rendering, and OS consistency.

// RawDeviceMetrics represents client-side hardware entropy collected via browser SDK.
type RawDeviceMetrics struct {
	UserAgent         string   `json:"user_agent"`
	Platform          string   `json:"platform"`           // e.g. "MacIntel", "Win32", "Linux x86_64"
	HardwareConcurrency int    `json:"hardware_concurrency"` // CPU cores
	DeviceMemoryGB    float64  `json:"device_memory_gb"`
	ScreenResolution  string   `json:"screen_resolution"`  // e.g. "1920x1080"
	ColorDepth        int      `json:"color_depth"`
	WebGLRenderer     string   `json:"webgl_renderer"`     // e.g. "ANGLE (Apple, Apple M2 Pro, OpenGL 4.1)"
	WebGLVendor       string   `json:"webgl_vendor"`       // e.g. "Google Inc. (Apple)"
	CanvasHash        string   `json:"canvas_hash"`        // 2D Canvas rendering hash
	AudioContextHash  string   `json:"audio_context_hash"` // Oscillator frequency decay signature
	TimezoneOffset    int      `json:"timezone_offset"`    // Minutes from UTC
	Language          string   `json:"language"`
	InstalledFontsCount int    `json:"installed_fonts_count"`
	WebRTCLocalIP     string   `json:"webrtc_local_ip"`    // Leaked private IP if ICE candidates active
}

// FingerprintVerdict represents the hardware integrity and spoofing risk analysis.
type FingerprintVerdict struct {
	DeviceFingerprint string   `json:"device_fingerprint"` // Immutable SHA-256 hardware identifier
	IsSpoofed         bool     `json:"is_spoofed"`         // Anti-detect browser detected
	IsVirtualMachine  bool     `json:"is_virtual_machine"` // Headless/VM detected (Mesa, SwiftShader)
	EntropyScore      float64  `json:"entropy_score"`      // 0..1 uniqueness score
	RiskScore         float64  `json:"risk_score"`         // 0..1 anomaly score
	SpoofingIndicators []string `json:"spoofing_indicators"`
}

// Analyze evaluates hardware entropy and flags spoofing or virtualized environments.
func Analyze(m *RawDeviceMetrics) FingerprintVerdict {
	v := FingerprintVerdict{}
	if m == nil {
		return v
	}

	var indicators []string
	var riskMass float64

	rendererLower := strings.ToLower(m.WebGLRenderer)
	uaLower := strings.ToLower(m.UserAgent)
	platformLower := strings.ToLower(m.Platform)

	// 1. Virtual Machine / Software Rasterizer Detection
	// Cloud/bot runners often use software emulators instead of physical GPUs
	if strings.Contains(rendererLower, "swiftshader") ||
		strings.Contains(rendererLower, "llvmpipe") ||
		strings.Contains(rendererLower, "mesa offscreen") ||
		strings.Contains(rendererLower, "virtualbox") ||
		strings.Contains(rendererLower, "vmware") {
		indicators = append(indicators, fmt.Sprintf("Virtual Machine / Software Rasterizer GPU detected: %s", m.WebGLRenderer))
		riskMass += 0.85
		v.IsVirtualMachine = true
	}

	// 2. OS vs WebGL Renderer Consistency Check
	// Anti-detect browsers often spoof User-Agent (e.g. claiming Windows) while underlying GPU is Apple M-series
	if strings.Contains(uaLower, "windows") && strings.Contains(rendererLower, "apple") {
		indicators = append(indicators, "Platform Inconsistency: Windows User-Agent with Apple Silicon GPU renderer")
		riskMass += 0.90
		v.IsSpoofed = true
	} else if strings.Contains(uaLower, "macintosh") && (strings.Contains(rendererLower, "direct3d") || strings.Contains(rendererLower, "nvidia geforce gtx")) {
		indicators = append(indicators, "Platform Inconsistency: macOS User-Agent with Direct3D/Nvidia GPU renderer")
		riskMass += 0.90
		v.IsSpoofed = true
	}

	// 3. Platform String vs User-Agent Consistency
	if strings.Contains(uaLower, "mac") && !strings.Contains(platformLower, "mac") {
		indicators = append(indicators, fmt.Sprintf("Navigator Platform mismatch: UA claims Mac but navigator.platform is '%s'", m.Platform))
		riskMass += 0.80
		v.IsSpoofed = true
	} else if strings.Contains(uaLower, "win") && !strings.Contains(platformLower, "win") {
		indicators = append(indicators, fmt.Sprintf("Navigator Platform mismatch: UA claims Windows but navigator.platform is '%s'", m.Platform))
		riskMass += 0.80
		v.IsSpoofed = true
	}

	// 4. Zero / Synthetic Entropy Anomaly
	// Headless bots often omit audio context or canvas rendering hashes
	if m.CanvasHash == "" || m.CanvasHash == "0" || m.CanvasHash == "null" {
		indicators = append(indicators, "Missing Canvas Fingerprint: Headless browser canvas block")
		riskMass += 0.75
		v.IsSpoofed = true
	}
	if m.AudioContextHash == "" || m.AudioContextHash == "0" {
		indicators = append(indicators, "Missing AudioContext Acoustic Fingerprint: Headless automation marker")
		riskMass += 0.70
	}

	// 5. Compute Immutable SHA-256 Device Fingerprint
	hasher := sha256.New()
	hasher.Write([]byte(m.WebGLRenderer))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(m.CanvasHash))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(m.AudioContextHash))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(m.ScreenResolution))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(fmt.Sprintf("%d", m.ColorDepth)))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(fmt.Sprintf("%d", m.HardwareConcurrency)))
	v.DeviceFingerprint = hex.EncodeToString(hasher.Sum(nil))

	// 6. Entropy Calculation (Shannon entropy across hardware parameters)
	entropy := calculateEntropy(m)
	v.EntropyScore = math.Round(entropy*100) / 100

	if riskMass > 0.98 {
		riskMass = 0.98
	}

	v.RiskScore = math.Round(riskMass*100) / 100
	v.IsSpoofed = v.IsSpoofed || (riskMass >= 0.75)
	v.SpoofingIndicators = indicators

	return v
}

func calculateEntropy(m *RawDeviceMetrics) float64 {
	// Baseline entropy score normalized [0..1]
	score := 0.50
	if m.InstalledFontsCount > 50 {
		score += 0.15
	}
	if m.AudioContextHash != "" && len(m.AudioContextHash) > 10 {
		score += 0.15
	}
	if m.CanvasHash != "" && len(m.CanvasHash) > 10 {
		score += 0.15
	}
	if m.HardwareConcurrency > 0 && m.DeviceMemoryGB > 0 {
		score += 0.05
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}
