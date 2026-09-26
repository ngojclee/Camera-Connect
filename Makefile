VERSION ?= 2.0.7.202604071812
UI_RUNTIME ?= harness
ALLOW_UI_FALLBACK ?= 0

.PHONY: all build build-wails build-wails-fallback installer installer-wails installer-wails-fallback e2e e2e-installer e2e-compare e2e-tray e2e-tray-wails e2e-tray-wails-blocker e2e-wails e2e-ui-parity agent ui test clean

all: build

build:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_windows.ps1 -Version "$(VERSION)" -UIRuntime "$(UI_RUNTIME)" $(if $(filter 1 yes true,$(ALLOW_UI_FALLBACK)),-AllowHarnessFallback,)

build-wails:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_windows.ps1 -Version "$(VERSION)" -UIRuntime "wails"

build-wails-fallback:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_windows.ps1 -Version "$(VERSION)" -UIRuntime "wails" -AllowHarnessFallback

installer:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_installer.ps1 -Version "$(VERSION)" -UIRuntime "$(UI_RUNTIME)" $(if $(filter 1 yes true,$(ALLOW_UI_FALLBACK)),-AllowHarnessFallback,)

installer-wails:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_installer.ps1 -Version "$(VERSION)" -UIRuntime "wails"

installer-wails-fallback:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/build_installer.ps1 -Version "$(VERSION)" -UIRuntime "wails" -AllowHarnessFallback

e2e:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_windows_manual.ps1 -Mode all

e2e-installer:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_installer_regression.ps1

e2e-compare:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_two_machine_compare.ps1

e2e-tray:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_tray_ui_smoke.ps1

e2e-tray-wails:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_tray_ui_smoke.ps1 -UIRuntime "wails"

e2e-tray-wails-blocker:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_tray_ui_smoke.ps1 -UIRuntime "wails" -AcceptKnownPreflightBlocker

e2e-wails:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_wails_ui_smoke.ps1

e2e-ui-parity:
	pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/e2e_ui_command_parity.ps1

agent:
	go build -ldflags "-X main.Version=$(VERSION)" -o build/bin/CameraConnectAgent.exe ./cmd/agent

ui:
	go build -ldflags "-X main.Version=$(VERSION)" -o build/bin/CameraConnectUI.exe ./cmd/ui

test:
	go test ./... -count=1

clean:
	pwsh -NoProfile -ExecutionPolicy Bypass -Command "if (Test-Path 'build/bin') { Remove-Item -LiteralPath 'build/bin' -Recurse -Force }"
