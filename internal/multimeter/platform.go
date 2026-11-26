package multimeter

import "strings"

// Platform identifies a multimeter release compatible with the requesting
// platform.
type Platform string

const (
	PlatformLinuxGNU   Platform = "linux-gnu"
	PlatformLinuxMusl  Platform = "linux-musl"
	PlatformWindowsX86 Platform = "windows-x86"
	PlatformWindowsARM Platform = "windows-arm"
	PlatformMismatch   Platform = "mismatch"
)

var executableFiles = map[Platform]string{
	PlatformLinuxGNU:   "multimeter-x86_64-unknown-linux-gnu.tar.gz",
	PlatformLinuxMusl:  "multimeter-x86_64-unknown-linux-musl.tar.gz",
	PlatformWindowsX86: "multimeter-x86_64-pc-windows-msvc.exe",
	PlatformWindowsARM: "multimeter-aarch64-pc-windows-msvc.exe",
}

func executableKey(platform Platform) (string, bool) {
	filename, ok := executableFiles[platform]
	if !ok {
		return "", false
	}
	return "multimeter/latest/" + filename, true
}

type architecture string

const (
	architectureUnknown architecture = ""
	architectureX86     architecture = "x86"
	architectureARM     architecture = "arm"
)

func classifyPlatform(userAgent, platformHint, architectureHint string) Platform {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	platform := normalizedHint(platformHint)
	archHint := normalizedHint(architectureHint)

	switch platform {
	case "windows":
		return classifyWindows(ua, archHint)
	case "linux":
		return classifyLinux(ua, archHint)
	case "":
		return classifyFromUserAgent(ua, archHint)
	default:
		return PlatformMismatch
	}
}

func classifyFromUserAgent(userAgent, architectureHint string) Platform {
	switch {
	case containsAny(userAgent, "android", "chrome os", "cros", "iphone", "ipad", "macintosh", "mac os x"):
		return PlatformMismatch
	case hasWindowsMarker(userAgent):
		return classifyWindows(userAgent, architectureHint)
	case strings.Contains(userAgent, "linux") || strings.Contains(userAgent, "x11"):
		return classifyLinux(userAgent, architectureHint)
	default:
		return PlatformMismatch
	}
}

func classifyWindows(userAgent, architectureHint string) Platform {
	arch, recognized := detectArchitecture(architectureHint, userAgent)
	if architectureHint != "" && !recognized {
		return PlatformMismatch
	}
	if arch == architectureARM {
		return PlatformWindowsARM
	}
	return PlatformWindowsX86
}

func classifyLinux(userAgent, architectureHint string) Platform {
	arch, recognized := detectArchitecture(architectureHint, userAgent)
	if architectureHint != "" && !recognized {
		return PlatformMismatch
	}
	if arch == architectureARM {
		return PlatformMismatch
	}
	if containsAny(userAgent, "musl", "alpine") {
		return PlatformLinuxMusl
	}
	return PlatformLinuxGNU
}

func detectArchitecture(architectureHint, userAgent string) (architecture, bool) {
	value := architectureHint
	if value == "" {
		value = userAgent
	}

	switch {
	case containsAny(value, "aarch64", "arm64", "armv8", "armhf", "armeabi", "arm"):
		return architectureARM, true
	case containsAny(value, "x86", "amd64", "x64", "i686", "i386", "win32", "win64"):
		return architectureX86, true
	default:
		return architectureUnknown, false
	}
}

func hasWindowsMarker(userAgent string) bool {
	return containsAny(userAgent, "windows", "win32", "win64", "winnt")
}

func normalizedHint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Trim(value, `"`)
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
