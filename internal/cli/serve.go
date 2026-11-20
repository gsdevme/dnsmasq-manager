package cli

import (
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/dnsmasq-manager/internal/api"
	"github.com/gsdevme/dnsmasq-manager/internal/config"
	"github.com/gsdevme/dnsmasq-manager/internal/health"
	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/hostsfile"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
	"github.com/gsdevme/dnsmasq-manager/internal/server"
	"github.com/gsdevme/dnsmasq-manager/internal/web"
	"github.com/gsdevme/dnsmasq-manager/internal/webhook"
)

// healthCacheTTL is how long one health check's result answers /healthz and
// the web page before the checks run again.
const healthCacheTTL = 2 * time.Second

// newServeCommand returns the serve subcommand. environ supplies the
// environment (os.Environ in production).
//
// serve loads its configuration and the managed file, binds every enabled
// listener, then serves until the command's context is cancelled, when it
// shuts down gracefully and returns nil. Any start-up problem is returned
// before anything is served. Logs are slog text lines on the command's
// stdout.
func newServeCommand(environ func() []string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the DNS records API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(environ())
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{Level: cfg.LogLevel}))
			state, err := hostsfile.File{Dir: cfg.HostsDir}.Load(cfg.Policy)
			if err != nil {
				return err
			}
			srv, err := server.Bind(listeners(cfg, state, logger))
			if err != nil {
				return err
			}
			return srv.Serve(cmd.Context(), logger)
		},
	}
}

// listeners builds the store over state and returns the REST listener and,
// when enabled, the webhook and web listeners, all sharing that store, one
// history log and one health checker whose result is reused for
// healthCacheTTL.
func listeners(cfg config.Config, state records.State, logger *slog.Logger) []server.Listener {
	store := records.NewStore(state, cfg.Policy, hostsfile.File{Dir: cfg.HostsDir})
	checker := health.NewCached(health.Checker{HostsDir: cfg.HostsDir, ProbeAddr: cfg.DNSProbeAddr.String()}, healthCacheTTL)
	hist := history.New(history.Size)

	ls := []server.Listener{{
		Name:    "api",
		Addr:    cfg.APIAddr,
		Handler: api.NewHandler(store, checker, cfg.APIToken, hist, logger),
		TLSCert: cfg.APITLSCert,
		TLSKey:  cfg.APITLSKey,
	}}
	if cfg.WebhookEnabled {
		ls = append(ls, server.Listener{
			Name:    "webhook",
			Addr:    cfg.WebhookAddr,
			Handler: webhook.NewHandler(store, cfg.WebhookAllowedSources, hist, logger),
		})
	}
	if cfg.WebEnabled {
		ls = append(ls, server.Listener{
			Name:    "web",
			Addr:    cfg.WebAddr,
			Handler: web.NewHandler(store, checker, hist),
		})
	}
	return ls
}
