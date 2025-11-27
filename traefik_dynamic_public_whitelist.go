package traefik_dynamic_public_whitelist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/traefik/genconf/dynamic"
	"github.com/traefik/genconf/dynamic/tls"
)

const (
	providerCloudflare  = "cloudflare"
	providerFastly      = "fastly"
	providerCloudfront  = "cloudfront"
	providerCustom      = "custom"
	awsCloudfrontLabel  = "CLOUDFRONT"
	defaultPollInterval = "300s"

	defaultCloudflareIPv4Endpoint = "https://www.cloudflare.com/ips-v4/"
	defaultCloudflareIPv6Endpoint = "https://www.cloudflare.com/ips-v6/"
	defaultFastlyEndpoint         = "https://api.fastly.com/public-ip-list"
	defaultAwsIPRangesEndpoint    = "https://ip-ranges.amazonaws.com/ip-ranges.json"
)

// Exported provider identifiers for users/tests.
const (
	ProviderCloudflare = providerCloudflare
	ProviderFastly     = providerFastly
	ProviderCloudfront = providerCloudfront
	ProviderCustom     = providerCustom
)

var (
	cloudflareIPv4Endpoint = defaultCloudflareIPv4Endpoint
	cloudflareIPv6Endpoint = defaultCloudflareIPv6Endpoint
	fastlyEndpoint         = defaultFastlyEndpoint
	awsIPRangesEndpoint    = defaultAwsIPRangesEndpoint
	supportedProviders     = map[string]struct{}{
		providerCloudflare: {},
		providerFastly:     {},
		providerCloudfront: {},
		providerCustom:     {},
	}
)

type httpGetter func(ctx context.Context, url string) ([]byte, error)

// Config the plugin configuration.
type Config struct {
	Provider              string   `json:"provider"`
	PollInterval          string   `json:"pollInterval,omitempty"`
	IPv4Resolver          string   `json:"ipv4Resolver,omitempty"`
	IPv6Resolver          string   `json:"ipv6Resolver,omitempty"`
	WhitelistIPv6         bool     `json:"whitelistIPv6,omitempty"`
	AdditionalSourceRange []string `json:"additionalSourceRange,omitempty"`
	IPStrategy            dynamic.IPStrategy
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		PollInterval:          defaultPollInterval,
		IPv4Resolver:          "https://api4.ipify.org/?format=text",
		IPv6Resolver:          "https://api6.ipify.org/?format=text",
		WhitelistIPv6:         false,
		AdditionalSourceRange: []string{},
		IPStrategy: dynamic.IPStrategy{
			Depth:       0,
			ExcludedIPs: nil,
		},
	}
}

// Provider a simple provider plugin.
type Provider struct {
	name                  string
	providerName          string
	pollInterval          time.Duration
	ipv4Resolver          string
	ipv6Resolver          string
	whitelistIPv6         bool
	additionalSourceRange []string
	ipStrategy            dynamic.IPStrategy
	httpGet               httpGetter

	cancel func()
}

// New creates a new Provider plugin.
func New(ctx context.Context, config *Config, name string) (*Provider, error) {
	pollInterval := config.PollInterval
	if strings.TrimSpace(pollInterval) == "" {
		pollInterval = defaultPollInterval
	}

	pi, err := time.ParseDuration(pollInterval)
	if err != nil {
		return nil, err
	}

	providerName := normalizeProviderName(config.Provider)
	if providerName == "" {
		return nil, fmt.Errorf("provider is required")
	}

	if _, ok := supportedProviders[providerName]; !ok {
		return nil, fmt.Errorf("unsupported provider %q", config.Provider)
	}

	if providerName == providerCustom {
		if strings.TrimSpace(config.IPv4Resolver) == "" {
			return nil, fmt.Errorf("custom provider requires an ipv4Resolver")
		}
		if config.WhitelistIPv6 && strings.TrimSpace(config.IPv6Resolver) == "" {
			return nil, fmt.Errorf("custom provider requires an ipv6Resolver when whitelistIPv6 is true")
		}
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}

	return &Provider{
		name:                  name,
		providerName:          providerName,
		pollInterval:          pi,
		ipv4Resolver:          config.IPv4Resolver,
		ipv6Resolver:          config.IPv6Resolver,
		whitelistIPv6:         config.WhitelistIPv6,
		additionalSourceRange: append([]string(nil), config.AdditionalSourceRange...),
		ipStrategy:            config.IPStrategy,
		httpGet:               defaultHTTPGetter(httpClient),
	}, nil
}

// Init the provider.
func (p *Provider) Init() error {
	if p.pollInterval <= 0 {
		return fmt.Errorf("poll interval must be greater than 0")
	}

	return nil
}

// Provide creates and send dynamic configuration.
func (p *Provider) Provide(cfgChan chan<- json.Marshaler) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Print(err)
			}
		}()

		p.loadConfiguration(ctx, cfgChan)
	}()

	return nil
}

func (p *Provider) loadConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	p.emitConfiguration(ctx, cfgChan)

	for {
		select {
		case <-ticker.C:
			p.emitConfiguration(ctx, cfgChan)
		case <-ctx.Done():
			return
		}
	}
}

func (p *Provider) emitConfiguration(ctx context.Context, cfgChan chan<- json.Marshaler) {
	configuration, err := p.generateConfiguration(ctx)
	if err != nil {
		log.Printf("traefik_dynamic_public_whitelist: failed to refresh configuration: %v", err)
		return
	}

	cfgChan <- &dynamic.JSONPayload{Configuration: configuration}
}

// Stop to stop the provider and the related go routines.
func (p *Provider) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}

	return nil
}

type IPAddresses struct {
	v4     string
	v6CIDR string
}

func ipv6ToCIDR(ipv6 string) (string, error) {
	const MaskSize = 64 // most providers supply 64 bit ipv6 addresses

	ip := net.ParseIP(ipv6)

	if ip.To4() != nil {
		return "", fmt.Errorf("input is not an IPv6 address: %s", ipv6)
	}

	ip = ip.To16()

	if ip == nil {
		return "", fmt.Errorf("input is not an IPv6 address: %s", ipv6)
	}

	cidr := ip.Mask(net.CIDRMask(MaskSize, 128)).String() + "/" + strconv.Itoa(MaskSize)

	return cidr, nil
}

func (p *Provider) generateConfiguration(ctx context.Context) (*dynamic.Configuration, error) {
	sourceRange, err := p.buildSourceRanges(ctx)
	if err != nil {
		return nil, err
	}

	configuration := &dynamic.Configuration{
		HTTP: &dynamic.HTTPConfiguration{
			Routers:           make(map[string]*dynamic.Router),
			Middlewares:       make(map[string]*dynamic.Middleware),
			Services:          make(map[string]*dynamic.Service),
			ServersTransports: make(map[string]*dynamic.ServersTransport),
		},
		TCP: &dynamic.TCPConfiguration{
			Routers:  make(map[string]*dynamic.TCPRouter),
			Services: make(map[string]*dynamic.TCPService),
		},
		TLS: &dynamic.TLSConfiguration{
			Stores:  make(map[string]tls.Store),
			Options: make(map[string]tls.Options),
		},
		UDP: &dynamic.UDPConfiguration{
			Routers:  make(map[string]*dynamic.UDPRouter),
			Services: make(map[string]*dynamic.UDPService),
		},
	}

	configuration.HTTP.Middlewares["public_ipwhitelist"] = &dynamic.Middleware{
		IPWhiteList: &dynamic.IPWhiteList{
			SourceRange: sourceRange,
			IPStrategy: &dynamic.IPStrategy{
				Depth:       p.ipStrategy.Depth,
				ExcludedIPs: p.ipStrategy.ExcludedIPs,
			},
		},
	}

	return configuration, nil
}

// GenerateConfiguration exposes generateConfiguration for testing and advanced scenarios.
func (p *Provider) GenerateConfiguration(ctx context.Context) (*dynamic.Configuration, error) {
	return p.generateConfiguration(ctx)
}

func (p *Provider) buildSourceRanges(ctx context.Context) ([]string, error) {
	providerRanges, err := p.fetchProviderRanges(ctx)
	if err != nil {
		return nil, err
	}

	sourceRange := append([]string{}, p.additionalSourceRange...)
	sourceRange = append(sourceRange, providerRanges...)

	if len(sourceRange) == 0 {
		return nil, fmt.Errorf("no source ranges resolved")
	}

	return sourceRange, nil
}

func (p *Provider) fetchProviderRanges(ctx context.Context) ([]string, error) {
	switch p.providerName {
	case providerCloudflare:
		return p.fetchCloudflareRanges(ctx)
	case providerFastly:
		return p.fetchFastlyRanges(ctx)
	case providerCloudfront:
		return p.fetchCloudfrontRanges(ctx)
	case providerCustom:
		return p.fetchCustomRanges(ctx)
	default:
		return nil, fmt.Errorf("unsupported provider %q", p.providerName)
	}
}

func (p *Provider) fetchCloudflareRanges(ctx context.Context) ([]string, error) {
	body, err := p.httpGet(ctx, cloudflareIPv4Endpoint)
	if err != nil {
		return nil, err
	}
	ranges := parseLineList(body)

	if len(ranges) == 0 {
		return nil, fmt.Errorf("cloudflare: empty IPv4 range list")
	}

	if p.whitelistIPv6 {
		body6, err := p.httpGet(ctx, cloudflareIPv6Endpoint)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, parseLineList(body6)...)
	}

	return ranges, nil
}

func (p *Provider) fetchFastlyRanges(ctx context.Context) ([]string, error) {
	body, err := p.httpGet(ctx, fastlyEndpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Addresses     []string `json:"addresses"`
		IPv6Addresses []string `json:"ipv6_addresses"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("fastly: %w", err)
	}

	ranges := append([]string{}, payload.Addresses...)
	if len(ranges) == 0 {
		return nil, fmt.Errorf("fastly: empty IPv4 addresses list")
	}

	if p.whitelistIPv6 {
		ranges = append(ranges, payload.IPv6Addresses...)
	}

	return ranges, nil
}

func (p *Provider) fetchCloudfrontRanges(ctx context.Context) ([]string, error) {
	body, err := p.httpGet(ctx, awsIPRangesEndpoint)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Prefixes []struct {
			IPPrefix string `json:"ip_prefix"`
			Service  string `json:"service"`
		} `json:"prefixes"`
		IPv6Prefixes []struct {
			IPv6Prefix string `json:"ipv6_prefix"`
			Service    string `json:"service"`
		} `json:"ipv6_prefixes"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("cloudfront: %w", err)
	}

	ranges := make([]string, 0)
	for _, prefix := range payload.Prefixes {
		if prefix.Service == awsCloudfrontLabel {
			ranges = append(ranges, strings.TrimSpace(prefix.IPPrefix))
		}
	}

	if len(ranges) == 0 {
		return nil, fmt.Errorf("cloudfront: empty IPv4 prefix set")
	}

	if p.whitelistIPv6 {
		for _, prefix := range payload.IPv6Prefixes {
			if prefix.Service == awsCloudfrontLabel {
				ranges = append(ranges, strings.TrimSpace(prefix.IPv6Prefix))
			}
		}
	}

	return ranges, nil
}

func (p *Provider) fetchCustomRanges(ctx context.Context) ([]string, error) {
	if strings.TrimSpace(p.ipv4Resolver) == "" {
		return nil, fmt.Errorf("custom provider requires an ipv4Resolver")
	}

	body, err := p.httpGet(ctx, p.ipv4Resolver)
	if err != nil {
		return nil, err
	}

	ipv4 := strings.TrimSpace(string(body))
	if net.ParseIP(ipv4) == nil {
		return nil, fmt.Errorf("custom provider: invalid IPv4 response")
	}

	ranges := []string{ipv4}

	if p.whitelistIPv6 {
		if strings.TrimSpace(p.ipv6Resolver) == "" {
			return nil, fmt.Errorf("custom provider requires an ipv6Resolver when whitelistIPv6 is true")
		}

		body6, err := p.httpGet(ctx, p.ipv6Resolver)
		if err != nil {
			return nil, err
		}

		ipv6 := strings.TrimSpace(string(body6))
		if net.ParseIP(ipv6) == nil {
			return nil, fmt.Errorf("custom provider: invalid IPv6 response")
		}

		ipv6CIDR, err := ipv6ToCIDR(ipv6)
		if err != nil {
			return nil, err
		}

		ranges = append(ranges, ipv6CIDR)
	}

	return ranges, nil
}

func parseLineList(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	results := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		results = append(results, line)
	}

	return results
}

func defaultHTTPGetter(client *http.Client) httpGetter {
	return func(ctx context.Context, url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Kes-RequestID", newRequestID())

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func(Body io.ReadCloser) {
			err := Body.Close()
			if err != nil {
				return
			}
		}(resp.Body)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, url)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		return body, nil
	}
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		timestamp := time.Now().UnixNano()
		return fmt.Sprintf("%032x", timestamp)
	}

	return hex.EncodeToString(buf[:])
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// The following setters help tests override external endpoints without touching private vars.
func SetCloudflareEndpoints(v4, v6 string) {
	if v4 != "" {
		cloudflareIPv4Endpoint = v4
	}
	if v6 != "" {
		cloudflareIPv6Endpoint = v6
	}
}

func SetFastlyEndpoint(url string) {
	if url != "" {
		fastlyEndpoint = url
	}
}

func SetAwsIPRangesEndpoint(url string) {
	if url != "" {
		awsIPRangesEndpoint = url
	}
}
