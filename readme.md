# Traefik Dynamic Public Whitelist Plugin

Create a Traefik middleware that automatically tracks the public IPs or CDN ranges you want to allow. The plugin periodically fetches the latest prefixes from trusted sources (or your own resolvers) and keeps the `IPWhiteList` middleware in sync without requiring manual reloads.

## Highlights

- **Provider-driven**: choose one or several of `cloudflare`, `fastly`, `cloudfront`, or `custom` (comma-separated) to decide where ranges originate.
- **IPv6 awareness**: toggle IPv6 independently; responses are normalized into `/64` prefixes when derived from single IPs.
- **Extra safety**: merge your own `additionalSourceRange` entries before emitting the middleware.
- **Deterministic headers**: every outbound HTTP call includes an `X-Kes-RequestID` header populated by a random 32-hex identifier for traceability.
- **Traefik native**: surfaces as `public_ipwhitelist@plugin-traefik_dynamic_public_whitelist`, so you can attach it just like any other middleware.

## Installation

1. Enable the plugin inside Traefik's **static** configuration.
2. Configure the plugin provider (still in static configuration) with the desired options.
3. Reference the middleware from your routers/services.

```yaml
# static configuration (example)
experimental:
  plugins:
    traefik_dynamic_public_whitelist:
      moduleName: github.com/KCL-Electronics/traefik_cdn_whitelist
      version: v0.1.0 # pin the commit/tag you trust

providers:
  plugin:
    traefik_dynamic_public_whitelist:
      provider: cloudflare,fastly    # required: accepts single value or comma list
      pollInterval: "120s"            # optional, defaults to 300s
      whitelistIPv6: true             # optional, defaults to false
      additionalSourceRange:
        - 192.168.0.0/24
      ipStrategy:                     # optional
        depth: 0
        excludedIPs: []
      # only used when provider: custom
      ipv4Resolver: https://api4.ipify.org/?format=text
      ipv6Resolver: https://api6.ipify.org/?format=text
```

### Runtime wiring

Attach the middleware wherever you need it (file provider, Docker labels, Kubernetes annotations, etc.).

```
labels:
  - traefik.http.routers.api.middlewares=public_ipwhitelist@plugin-traefik_dynamic_whitelist
```

## Configuration Reference

| Setting | Required | Description |
| --- | --- | --- |
| `provider` | ✅ | Determines which backend is queried (single value or comma-separated mix of `cloudflare`, `fastly`, `cloudfront`, `custom`). |
| `pollInterval` | ❌ | How often to refresh ranges. Supports Go duration strings (`300s`, `10m`). |
| `whitelistIPv6` | ❌ | Include IPv6 data from the provider/custom resolvers. |
| `additionalSourceRange` | ❌ | CIDRs appended to the provider ranges. Useful for office IPs or VPN blocks. |
| `ipStrategy.depth` | ❌ | Traefik forwarding depth when trusting `X-Forwarded-For`. |
| `ipStrategy.excludedIPs` | ❌ | Addresses ignored during depth evaluation. |
| `ipv4Resolver` / `ipv6Resolver` | ✅ for `custom` | URLs returning your public IPv4/IPv6 addresses (plain text). Required when provider is `custom` (`ipv6Resolver` only when `whitelistIPv6` is true). |

## Provider Behavior

| Provider | Sources | Notes |
| --- | --- | --- |
| `cloudflare` | `https://www.cloudflare.com/ips-v4/` and `https://www.cloudflare.com/ips-v6/` | IPv6 list is ignored unless `whitelistIPv6` is true. |
| `fastly` | `https://api.fastly.com/public-ip-list` | Parses `addresses` (IPv4) and `ipv6_addresses`. |
| `cloudfront` | `https://ip-ranges.amazonaws.com/ip-ranges.json` | Filters entries whose `service` equals `CLOUDFRONT`. |
| `custom` | User-defined resolvers | Each resolver must return a single textual IP. IPv6 responses are converted to `/64`.

### Custom Provider Walkthrough

```yaml
providers:
  plugin:
    traefik_dynamic_public_whitelist:
      provider: custom
      ipv4Resolver: http://metadata/ipv4
      ipv6Resolver: http://metadata/ipv6
      whitelistIPv6: true
      pollInterval: "30s"
      additionalSourceRange:
        - 203.0.113.10/32
```

1. The plugin fetches the IPv4/IPv6 addresses from the resolvers.
2. IPv6 is normalized to a `/64` network using the first 64 bits.
3. Custom ranges are prepended with any `additionalSourceRange` entries.
4. The middleware is emitted and pushed to Traefik.

## Request Lifecycle

- A ticker dispatches refreshes based on `pollInterval` (minimum > 0).
- Each HTTP request carries `X-Kes-RequestID: <random-32-hex>` to help log correlation.
- Non-2xx responses or malformed payloads are logged; the previous successful configuration remains active.

## Testing the Plugin Locally

```bash
# run the Go unit tests
cd /path/to/traefik_cdn_whitelist
go test ./...
```

### Release & Publishing

With Go 1.25 toolchain:

1. `go mod tidy` and `go mod vendor` to pin dependencies.
2. `go test ./...` followed by `make yaegi_test` to ensure native + Yaegi compatibility.
3. Tag the repository (`git tag vX.Y.Z && git push origin vX.Y.Z`).
4. Update the Traefik catalog entry if needed and bump the version in static config snippets.

## Troubleshooting Tips

- Ensure the Traefik process can reach the provider endpoints; failures show up in the plugin logs.
- When using the `custom` provider, verify the resolver responses are raw IPs (no newline noise).
- Use `additionalSourceRange` for emergency lockouts if a provider endpoint becomes unavailable.

## Further Reading

- [Traefik Pilot Plugins](https://doc.traefik.io/traefik-pilot/plugins/overview/)
- [Traefik Middleware Reference](https://doc.traefik.io/traefik/middlewares/overview/)
