// Command ui serves the read-only web UI of the ai-operator.
package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
	"github.com/jonathanwiemers/ai-operator/internal/ui"
	"github.com/jonathanwiemers/ai-operator/web"
)

func main() {
	var configFile, bindAddr, probeAddr string
	var historyRetention time.Duration
	flag.StringVar(&configFile, "config", "/etc/ai-operator-ui/config.yaml", "The UI's config file.")
	flag.StringVar(&bindAddr, "bind-address", ":9080", "The address the UI binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.DurationVar(&historyRetention, "history-retention", history.DefaultRetention, "How long Desk chat messages are kept (expires_at on insert); match the operator's --history-retention.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("ui")

	cfg, err := ui.LoadConfig(configFile, bindAddr)
	if err != nil {
		log.Error(err, "invalid configuration")
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(aiv1.AddToScheme(scheme))
	c, err := cache.New(ctrl.GetConfigOrDie(), cache.Options{
		Scheme:           scheme,
		DefaultTransform: cache.TransformStripManagedFields(),
	})
	if err != nil {
		log.Error(err, "unable to create cache")
		os.Exit(1)
	}
	ctx := ctrl.SetupSignalHandler()
	hub := ui.NewHub()
	if err := ui.Watch(ctx, c, hub, cfg.Research.Enabled()); err != nil {
		log.Error(err, "unable to watch resources")
		os.Exit(1)
	}
	go func() {
		if err := c.Start(ctx); err != nil {
			log.Error(err, "cache failed")
			os.Exit(1)
		}
	}()
	var synced atomic.Bool
	go func() { synced.Store(c.WaitForCacheSync(ctx)) }()

	srv, err := ui.NewServer(cfg, c, hub, web.App(), log)
	if err != nil {
		log.Error(err, "unable to create server")
		os.Exit(1)
	}
	// Live transcripts read the agent pods' logs (the chart grants pods/log
	// in the agent namespaces).
	clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		log.Error(err, "unable to create a Kubernetes client")
		os.Exit(1)
	}
	srv.SetLogSource(ui.PodLogs{Client: clientset})
	srv.SetHistoryRetention(historyRetention)
	// One-off researches are created and deleted directly, not through the
	// cache. Only with allowCreate: the chart grants create/delete then.
	if cfg.Research.Enabled() && cfg.Research.AllowCreate {
		writer, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
		if err != nil {
			log.Error(err, "unable to create the research writer client")
			os.Exit(1)
		}
		srv.SetResearchWriter(writer)
	}
	// Research reads the database as the reader (the operator migrates it).
	// It connects in the background, so the factory does not wait for it.
	if cfg.Research.Enabled() {
		go srv.ConnectResearch(ctx, func(ctx context.Context) (*store.Store, error) {
			return store.Open(ctx, cfg.ResearchLogin())
		})
	}
	// Desk chat inserts use the chat writer login (row-level insert of user
	// rows only). Same database host as the reader.
	if cfg.ChatEnabled() {
		go srv.ConnectChat(ctx, func(ctx context.Context) (*store.Store, error) {
			return store.Open(ctx, cfg.ChatLogin())
		})
	}

	probes := http.NewServeMux()
	probes.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	probes.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !synced.Load() {
			http.Error(w, "cache not synced", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	servers := []*http.Server{
		// No write timeout: the event stream stays open. Its requests end
		// with ctx, so they do not hold up the shutdown.
		{
			Addr: bindAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			BaseContext: func(net.Listener) context.Context { return ctx },
		},
		{Addr: probeAddr, Handler: probes, ReadHeaderTimeout: 10 * time.Second},
	}
	for _, s := range servers {
		go func() {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error(err, "server failed", "address", s.Addr)
				os.Exit(1)
			}
		}()
	}
	log.Info("serving", "address", bindAddr, "auth", cfg.Auth.Mode, "externalURL", cfg.ExternalURL)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
}
