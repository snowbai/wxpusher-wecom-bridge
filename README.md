# WxPusher WeCom Bridge

Go service that receives WxPusher messages using the same protocol shape as the Chrome extension, persists messages locally, forwards originals to a WeCom robot, and enriches linked messages with Headless Chrome text extraction and screenshots.

## Safety Notes

- The bridge does not read or send Chrome cookies.
- The bridge only imports explicit identity fields from a specified Chrome profile or JSON file.
- The WxPusher protocol implementation keeps the extension's headers and WebSocket parameters: `platform`, `version`, `deviceToken`, and optional `pushToken`.
- Logs and import output redact `deviceToken` and `pushToken`.

## Quick Start

```bash
go build -o bin/wxpusher-bridge ./cmd/wxpusher-bridge
cp configs/config.example.toml config.local.toml
export WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...'
bin/wxpusher-bridge import-json -config config.local.toml -file identity.local.json
bin/wxpusher-bridge run -config config.local.toml
```

To import from the installed Chrome extension instead of JSON:

```bash
bin/wxpusher-bridge import-chrome -config config.local.toml -profile "/path/to/Profile" -extension-id "<id>"
```

## Server Deployment

1. Build or copy the binary to `/usr/local/bin/wxpusher-bridge`.
2. Create a service user and data directory:

```bash
sudo useradd --system --home /var/lib/wxpusher-bridge --shell /usr/sbin/nologin wxpusher-bridge
sudo mkdir -p /etc/wxpusher-bridge /var/lib/wxpusher-bridge/screenshots
sudo chown -R wxpusher-bridge:wxpusher-bridge /var/lib/wxpusher-bridge
```

3. Copy `configs/config.example.toml` to `/etc/wxpusher-bridge/config.toml` and adjust paths, Chrome binary, and retry settings.
4. Store the WeCom webhook outside the TOML file:

```bash
sudo install -m 0600 /dev/null /etc/wxpusher-bridge/env
echo 'WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...' | sudo tee /etc/wxpusher-bridge/env
```

5. Import the identity before starting the service:

```bash
sudo -u wxpusher-bridge /usr/local/bin/wxpusher-bridge import-json -config /etc/wxpusher-bridge/config.toml -file /path/to/identity.local.json
```

6. Install and start the unit:

```bash
sudo cp docs/systemd/wxpusher-bridge.service /etc/systemd/system/wxpusher-bridge.service
sudo systemctl daemon-reload
sudo systemctl enable --now wxpusher-bridge
sudo journalctl -u wxpusher-bridge -f
```

See `docs/acceptance.md` for manual verification.
