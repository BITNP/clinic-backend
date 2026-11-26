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
	PlatformLinuxGNU:   "multimeter-x86_64-unknown-linux-gnu",
	PlatformLinuxMusl:  "multimeter-x86_64-unknown-linux-musl",
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
	architectureUnknown     architecture = ""
	architectureX86         architecture = "x86"
	architectureARM         architecture = "arm"
	architectureUnsupported architecture = "unsupported"
)

func classifyPlatform(userAgent, platformHint, architectureHint string) Platform {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	platform := normalizedHint(platformHint)
	archHint := normalizedHint(architectureHint)

	if hasUnsupportedUserAgentMarker(ua) {
		return PlatformMismatch
	}

	switch platform {
	case "windows":
		if hasLinuxMarker(ua) {
			return PlatformMismatch
		}
		return classifyWindows(ua, archHint)
	case "linux":
		if hasWindowsMarker(ua) {
			return PlatformMismatch
		}
		return classifyLinux(ua, archHint)
	case "":
		return classifyFromUserAgent(ua, archHint)
	default:
		return PlatformMismatch
	}
}

func classifyFromUserAgent(userAgent, architectureHint string) Platform {
	switch {
	case hasWindowsMarker(userAgent) && hasLinuxMarker(userAgent):
		return PlatformMismatch
	case hasWindowsMarker(userAgent):
		return classifyWindows(userAgent, architectureHint)
	case hasLinuxMarker(userAgent):
		return classifyLinux(userAgent, architectureHint)
	default:
		return PlatformMismatch
	}
}

func classifyWindows(userAgent, architectureHint string) Platform {
	switch detectArchitecture(architectureHint, userAgent) {
	case architectureARM:
		return PlatformWindowsARM
	case architectureX86:
		return PlatformWindowsX86
	default:
		return PlatformMismatch
	}
}

func classifyLinux(userAgent, architectureHint string) Platform {
	if detectArchitecture(architectureHint, userAgent) != architectureX86 {
		return PlatformMismatch
	}
	if containsAny(userAgent, "musl", "alpine") {
		return PlatformLinuxMusl
	}
	return PlatformLinuxGNU
}

func detectArchitecture(architectureHint, userAgent string) architecture {
	userAgentArchitecture := detectUserAgentArchitecture(userAgent)
	// Architecture hints describe families such as x86 or arm, not bitness.
	// An explicit 32-bit UA marker must still reject the request.
	if userAgentArchitecture == architectureUnsupported {
		return architectureUnsupported
	}
	if architectureHint != "" {
		return detectArchitectureHint(architectureHint)
	}
	return userAgentArchitecture
}

func detectArchitectureHint(value string) architecture {
	switch {
	case containsAny(value, "arm64", "aarch64", "armv8"):
		return architectureARM
	case containsAny(value, "arm32", "armhf", "armv7", "armv6", "armeabi"):
		return architectureUnsupported
	case value == "arm":
		return architectureARM
	case containsAny(value, "x86_64", "x86-64", "amd64", "x64", "win64"):
		return architectureX86
	case value == "x86":
		return architectureX86
	case containsAny(value, "i386", "i486", "i586", "i686", "win32", "x86_32", "x86-32"):
		return architectureUnsupported
	default:
		return architectureUnknown
	}
}

func detectUserAgentArchitecture(value string) architecture {
	switch {
	case containsAny(value, "i386", "i486", "i586", "i686", "win32", "x86_32", "x86-32"):
		return architectureUnsupported
	case containsAny(value, "x86_64", "x86-64", "amd64", "x64", "win64"):
		return architectureX86
	case containsAny(value, "aarch64", "arm64"):
		return architectureARM
	case containsAny(value, "arm32", "armhf", "armv7", "armv6", "armeabi"):
		return architectureUnsupported
	case containsUserAgentToken(value, "x86") || containsUserAgentToken(value, "arm"):
		return architectureUnsupported
	default:
		return architectureUnknown
	}
}

func hasWindowsMarker(userAgent string) bool {
	return containsAny(userAgent, "windows", "win32", "win64", "winnt")
}

func hasLinuxMarker(userAgent string) bool {
	return strings.Contains(userAgent, "linux")
}

func containsUserAgentToken(userAgent, token string) bool {
	fields := strings.FieldsFunc(userAgent, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == ',' || r == '/' || r == ';'
	})
	for _, field := range fields {
		if field == token {
			return true
		}
	}
	return false
}

func hasUnsupportedUserAgentMarker(userAgent string) bool {
	return containsAny(
		userAgent,
		"android",
		"chrome os",
		"chromeos",
		"cros",
		"freebsd",
		"iphone",
		"ipad",
		"ipod",
		"macintosh",
		"mac os x",
		"netbsd",
		"nintendo",
		"openbsd",
		"playstation",
		"solaris",
		"sunos",
		"windows phone",
	)
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
