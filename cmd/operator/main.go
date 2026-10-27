// Command operator runs the ai-operator controllers.
package main

import (
	"flag"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/controller"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/webhook"
)

func main() {
	var metricsAddr, probeAddr string
	var leaderElect bool
	var defaultAgentImage string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for the controller manager.")
	flag.StringVar(&defaultAgentImage, "default-agent-image", "", "Agent image for AgentProjects that do not set spec.agents.image.")
	var defaultOwners string
	flag.StringVar(&defaultOwners, "default-owners", "", "Comma separated GitLab usernames mentioned when a task needs a human, for AgentProjects that do not set spec.notify.users.")
	var agentLogLevel string
	flag.StringVar(&agentLogLevel, "agent-log-level", "info", "Log level (debug, info or error) of the agent pods of AgentProjects that do not set spec.agents.logLevel.")
	var webhookAddr string
	flag.StringVar(&webhookAddr, "webhook-bind-address", "", "The address the GitLab webhook receiver (POST "+webhook.Path+") binds to, on the leader only. Empty: no receiver.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")
	switch aiv1.LogLevel(agentLogLevel) {
	case aiv1.LogLevelDebug, aiv1.LogLevelInfo, aiv1.LogLevelError:
	default:
		setupLog.Error(nil, "invalid --agent-log-level, want debug, info or error", "value", agentLogLevel)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(aiv1.AddToScheme(scheme))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "ai-operator.ai.wiemers.me",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	projects := &controller.AgentProjectReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		NewGitLab: func(baseURL, token string) gitlab.API {
			return gitlab.New(baseURL, token)
		},
		DefaultOwners: notify.Usernames(strings.Split(defaultOwners, ",")),
	}
	if webhookAddr != "" {
		// The bot's own writes cause deliveries too: one sync per project
		// per 5 s, with a trailing one for the last delivery.
		waker := webhook.NewWaker(5*time.Second, 100, clock.RealClock{})
		projects.Wakes = waker.Events()
		utilruntime.Must(mgr.Add(&webhook.Server{
			Addr:    webhookAddr,
			Handler: &webhook.Receiver{Reader: mgr.GetClient(), Wake: waker.Wake, Log: ctrl.Log.WithName("webhook")},
		}))
	}
	if err := projects.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentProject")
		os.Exit(1)
	}
	if err := (&controller.AgentTaskReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		DefaultAgentImage:    defaultAgentImage,
		DefaultAgentLogLevel: aiv1.LogLevel(agentLogLevel),
		APIReader:            mgr.GetAPIReader(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "AgentTask")
		os.Exit(1)
	}

	utilruntime.Must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	utilruntime.Must(mgr.AddReadyzCheck("readyz", healthz.Ping))

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
