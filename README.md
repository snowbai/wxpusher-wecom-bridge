# WxPusher WeCom Bridge

Go sidecar for an existing WxPusher Chrome extension. The extension keeps the original WxPusher network connection, while this bridge listens to Chrome DevTools Protocol WebSocket events, persists messages locally, forwards originals to a WeCom robot, and enriches linked messages with page text and screenshots.

## Why Chrome CDP

The bridge does not recreate WxPusher HTTP or WebSocket requests. Chrome and the installed extension still own:

- WebSocket handshake headers
- `User-Agent`
- `Accept-Language`
- cookies and credential policy
- `Origin: chrome-extension://...`
- TLS and browser network fingerprint

The Go process only observes frames through CDP and handles storage, WeCom delivery, and enrichment.

## Mac Quick Start

1. Create a dedicated Chrome data directory for the bridge.

Chrome 136 and newer reject DevTools remote debugging on the default Chrome data directory. Do not use the normal `~/Library/Application Support/Google/Chrome` profile for this sidecar.

```bash
mkdir -p "$HOME/wxpusher-bridge-chrome-profile"
```

2. Start Chrome with local CDP enabled and the dedicated data directory:


```bash
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port=9222 \
  --user-data-dir="$HOME/wxpusher-bridge-chrome-profile"
```

3. In that Chrome window, install or enable the WxPusher extension and bind it once. This dedicated profile is persistent; you do not need to bind again unless the extension identity expires.

4. Confirm CDP is reachable:

```bash
curl -sS http://127.0.0.1:9222/json/version
```

5. Get the WxPusher extension ID from `chrome://extensions`, then update `config.local.toml`:

```bash
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
cp configs/config.example.toml config.local.toml
```

Set:

```toml
[receiver]
mode = "chrome-cdp"
cdp_url = "http://127.0.0.1:9222"
extension_id = "<your installed WxPusher extension id>"
```

For a local Mac run, keep storage under a writable local directory:

```toml
[storage]
sqlite_path = "./wxpusher-bridge-data/bridge.db"

[browser]
screenshot_dir = "./wxpusher-bridge-data/screenshots"
```

6. Set the WeCom webhook and run:

```bash
export WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
bin/wxpusher-bridge run -config config.local.toml
```

The bridge is ready only after it attaches to the extension service worker and observes the WxPusher WebSocket.

## LaunchAgent

The included examples under `docs/launchd/` are user-level LaunchAgents:

- `com.wxpusher.chrome-debug.plist`: starts Chrome with CDP on `127.0.0.1:9222`.
- `com.wxpusher.bridge.plist`: starts the Go bridge.

Install them under `~/Library/LaunchAgents` after adjusting paths and webhook values.

## Fallback Mode

`receiver.mode = "go-fallback"` keeps the earlier pure-Go WxPusher client available for debugging. It is not the default and does not satisfy the goal of matching the Chrome extension's network behavior.

See `docs/acceptance.md` for manual verification.
