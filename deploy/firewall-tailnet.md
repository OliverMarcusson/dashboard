# Tailnet-only firewall intent

The dashboard app binds to `127.0.0.1:3000`; only Caddy listens on HTTPS.

Allow HTTPS only via the Tailscale interface:

```bash
sudo firewall-cmd --permanent --zone=trusted --add-interface=tailscale0
sudo firewall-cmd --permanent --zone=trusted --add-service=https
sudo firewall-cmd --permanent --zone=public --remove-service=https || true
sudo firewall-cmd --reload
```

If SSH is managed separately, do not change that policy blindly.

Verification from outside Tailnet:

```bash
curl -I https://dash.olivermarcusson.se
# should fail or time out
```

Verification from inside Tailnet:

```bash
curl -I https://dash.olivermarcusson.se
# should return Caddy/dashboard response
```
