package updater

import "testing"

func TestDetectManagedInstall(t *testing.T) {
	cases := []struct {
		name    string
		exePath string
		wantMgr string
		wantCmd string
	}{
		{
			name:    "standard macOS Applications bundle",
			exePath: "/Applications/Hoxi.app/Contents/MacOS/Hoxi",
			wantMgr: "",
			wantCmd: "",
		},
		{
			name:    "homebrew caskroom macOS bundle",
			exePath: "/opt/homebrew/Caskroom/hoxi/0.7.0/Hoxi.app/Contents/MacOS/Hoxi",
			wantMgr: "homebrew",
			wantCmd: "brew upgrade --cask hoxi",
		},
		{
			name:    "homebrew cellar formula binary",
			exePath: "/usr/local/Cellar/hoxi/0.7.0/bin/hoxi",
			wantMgr: "homebrew",
			wantCmd: "brew upgrade hoxi",
		},
		{
			name:    "nix store binary",
			exePath: "/nix/store/9k2abc123-hoxi-0.7.0/bin/hoxi",
			wantMgr: "nix",
			wantCmd: "nix profile upgrade hoxi",
		},
		{
			name:    "windows scoop install",
			exePath: `C:\Users\dev\scoop\apps\hoxi\current\Hoxi.exe`,
			wantMgr: "scoop",
			wantCmd: "scoop update hoxi",
		},
		{
			name:    "windows winget package dir",
			exePath: `C:\Users\dev\AppData\Local\Microsoft\WinGet\Packages\Hoxi.Hoxi_Microsoft.Winget.Source\Hoxi.exe`,
			wantMgr: "winget",
			wantCmd: "winget upgrade Hoxi.Hoxi",
		},
		{
			name:    "windows standard localappdata install",
			exePath: `C:\Users\dev\AppData\Local\Programs\Hoxi\Hoxi.exe`,
			wantMgr: "",
			wantCmd: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectManagedInstall(tc.exePath)
			if got.Manager != tc.wantMgr || got.UpgradeCommand != tc.wantCmd {
				t.Fatalf("DetectManagedInstall(%q) = %+v; want manager=%q cmd=%q", tc.exePath, got, tc.wantMgr, tc.wantCmd)
			}
		})
	}
}
