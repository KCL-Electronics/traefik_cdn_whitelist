# Traefik 动态公共白名单插件

通过该 Traefik 插件可以自动维护允许访问的公共 IP 或 CDN 网段，持续同步最新的 IP 白名单配置，而无需每次手动更新 Traefik 配置。

## 功能亮点

- **多种来源**：支持 `cloudflare`、`fastly`、`cloudfront`、`custom` 四种 Provider，覆盖主流 CDN 或自定义业务场景。
- **IPv6 支持**：可独立开启/关闭 IPv6；若 Provider 仅返回单个 IPv6 地址，会自动转换为 `/64` 前缀。
- **附加网段**：可通过 `additionalSourceRange` 追加企业办公 IP、VPN 等自定义网段。
- **请求可追踪**：所有对外 HTTP 请求都会带上 `X-Kes-RequestID` 头，值为随机 32 位十六进制字符串，便于排查和日志关联。
- **原生 Traefik 中间件**：生成 `public_ipwhitelist@plugin-traefik_dynamic_public_whitelist`，可直接在路由/服务中引用。

## 安装步骤

1. 在 Traefik **静态配置** 中启用插件。
2. 同样在静态配置里设置 Provider、轮询周期等参数。
3. 在路由或服务上引用该中间件。

```yaml
# 静态配置示例
experimental:
  plugins:
    traefik_dynamic_public_whitelist:
      moduleName: github.com/KCL-Electronics/traefik_cdn_whitelist
      version: v0.1.0  # 请锁定可信版本

providers:
  plugin:
    traefik_dynamic_public_whitelist:
      provider: cloudflare            # 必填: cloudflare|fastly|cloudfront|custom
      pollInterval: "120s"            # 选填, 默认 300s
      whitelistIPv6: true             # 选填, 默认 false
      additionalSourceRange:
        - 192.168.0.0/24
      ipStrategy:                     # 选填
        depth: 0
        excludedIPs: []
      # provider=custom 时需要
      ipv4Resolver: https://api4.ipify.org/?format=text
      ipv6Resolver: https://api6.ipify.org/?format=text
```

### 运行时引用

可以在 Docker 标签、Kubernetes 注解或文件提供器中绑定该中间件，例如：

```
labels:
  - traefik.http.routers.api.middlewares=public_ipwhitelist@plugin-traefik_dynamic_public_whitelist
```

## 配置说明

| 配置项 | 是否必填 | 说明 |
| --- | --- | --- |
| `provider` | ✅ | 选择网段来源：`cloudflare`、`fastly`、`cloudfront`、`custom`。 |
| `pollInterval` | ❌ | 刷新频率，支持 Go Duration（`300s`、`10m` 等）。 |
| `whitelistIPv6` | ❌ | 是否包含 IPv6 数据。 |
| `additionalSourceRange` | ❌ | 自定义追加 CIDR 列表。 |
| `ipStrategy.depth` | ❌ | Traefik 处理 `X-Forwarded-For` 时使用的深度。 |
| `ipStrategy.excludedIPs` | ❌ | 忽略的 IP 列表。 |
| `ipv4Resolver` / `ipv6Resolver` | ✅（`custom`） | 返回纯文本 IP 的 HTTP 地址。IPv6 Resolver 仅在开启 `whitelistIPv6` 时必填。 |

## Provider 行为

| Provider | 数据源 | 备注 |
| --- | --- | --- |
| `cloudflare` | `https://www.cloudflare.com/ips-v4/`、`https://www.cloudflare.com/ips-v6/` | 未开启 `whitelistIPv6` 时不会请求 IPv6。 |
| `fastly` | `https://api.fastly.com/public-ip-list` | 解析 JSON 中的 `addresses` 与 `ipv6_addresses`。 |
| `cloudfront` | `https://ip-ranges.amazonaws.com/ip-ranges.json` | 仅保留 `service == CLOUDFRONT` 的条目。 |
| `custom` | 用户自定义接口 | 需返回单个 IPv4/IPv6；IPv6 自动转 `/64`。

### Custom Provider 示例

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

1. 定期访问 `ipv4Resolver`/`ipv6Resolver`，拿到最新 IP。
2. IPv6 自动转换为 `/64` CIDR。
3. 将自定义网段与 resolver 结果合并。
4. 生成 `IPWhiteList` 中间件并推送给 Traefik。

## 请求流程

- 依据 `pollInterval` 启动定时器刷新数据。
- 所有 HTTP 请求都会带 `X-Kes-RequestID` 头。
- 若请求失败或数据不合法，会记录日志并保留上一份生效配置。

## 本地测试

```bash
cd /path/to/traefik_cdn_whitelist
go test ./...
```

### 发布与推送

使用 Go 1.25 工具链：

1. 执行 `go mod tidy && go mod vendor`，确保依赖锁定。
2. 运行 `go test ./...` 以及 `make yaegi_test`，验证本地与 Yaegi 兼容性。
3. 创建并推送新标签（例如 `git tag vX.Y.Z && git push origin vX.Y.Z`）。
4. 如需更新 Traefik Catalog，请同步 README 示例中的版本号。

## 排查建议

- 确保 Traefik 能访问相关 Provider 接口；失败信息会打印在插件日志中。
- `custom` 模式下请确认返回内容仅为 IP，避免额外换行或 JSON。
- 若 Provider 暂不可用，可临时依赖 `additionalSourceRange` 保障访问。

## 延伸阅读

- [Traefik 插件官方文档](https://doc.traefik.io/traefik-pilot/plugins/overview/)
- [Traefik Middleware 说明](https://doc.traefik.io/traefik/middlewares/overview/)
