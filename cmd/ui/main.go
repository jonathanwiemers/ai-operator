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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/ui"
	"github.com/jonathanwiemers/ai-operator/web"
)

func main() {
	var configFile, bindAddr, probeAddr string
	flag.StringVar(&configFile, "config", "/etc/ai-operator-ui/config.yaml", "The UI's config file.")
	flag.StringVar(&bindAddr, "bind-address", ":9080", "The address the UI binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
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
	if err := ui.Watch(ctx, c, hub); err != nil {
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
