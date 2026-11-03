// Command operator runs the ai-operator controllers.
package main

import (
	"errors"
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
	"github.com/jonathanwiemers/ai-operator/internal/history"
	"github.com/jonathanwiemers/ai-operator/internal/notify"
	"github.com/jonathanwiemers/ai-operator/internal/research"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
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
	var researchDB research.DatabaseConfig
	flag.StringVar(&researchDB.Host, "research-db-host", "", "Host of the research database. Empty: research is off. The owner login is read from AI_RESEARCH_DB_USER and AI_RESEARCH_DB_PASSWORD.")
	flag.IntVar(&researchDB.Port, "research-db-port", 5432, "Port of the research database.")
	flag.StringVar(&researchDB.Name, "research-db-name", "", "Name of the research database.")
	flag.StringVar(&researchDB.SSLMode, "research-db-sslmode", "require", "sslmode of the research database: disable, require, verify-ca or verify-full.")
	var researchGrants store.Grants
	var researchWriters string
	flag.StringVar(&researchGrants.Reader, "research-reader-role", "", "Role the research migrations grant read access to (the UI's login).")
	flag.StringVar(&researchWriters, "research-writer-roles", "", "Comma separated roles the research migrations grant write access to (the logins of research runs).")
	flag.StringVar(&researchGrants.ChatWriter, "research-chat-writer-role", "", "Role the research migrations grant chat insert access to (the UI's chat login).")
	var quickCandidates, deepCandidates string
	var maxUsagePercent int
	flag.StringVar(&quickCandidates, "research-candidates-quick", "claude/claude-sonnet-5-5,cursor/auto", "Agents quick research runs without spec.agent try in order, comma separated engine/model[@effort]. AgentProject spec.research.candidates overrides them.")
	flag.StringVar(&deepCandidates, "research-candidates-deep", "claude/claude-opus-5-5@high,claude/claude-sonnet-5-5,cursor/auto", "Agents deep research runs without spec.agent try in order, like --research-candidates-quick.")
	flag.IntVar(&maxUsagePercent, "research-max-usage-percent", aiv1.DefaultResearchMaxUsagePercent, "Research runs on an agent only while every usage window of its engine is below this percentage (1-100); the rest is left to the factory.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	var historyRetention time.Duration
	flag.DurationVar(&historyRetention, "history-retention", history.DefaultRetention, "How long the factory's history (comments, runs, CI, reviews, merges) and Desk chat messages are kept in the research database.")
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")
	switch aiv1.LogLevel(agentLogLevel) {
	case aiv1.LogLevelDebug, aiv1.LogLevelInfo, aiv1.LogLevelError:
	default:
		setupLog.Error(nil, "invalid --agent-log-level, want debug, info or error", "value", agentLogLevel)
		os.Exit(1)
	}
	// Research is on exactly with a database host. The research controller
	// waits for researchSetup's Ready and uses researchDB for its pods.
	var candidates aiv1.ResearchCandidates
	var researchSetup *research.Database
	if researchDB.Host != "" {
		owner, err := researchDB.Login(os.Getenv("AI_RESEARCH_DB_USER"), os.Getenv("AI_RESEARCH_DB_PASSWORD"))
		if err != nil {
			setupLog.Error(err, "invalid research database configuration (--research-db-*, AI_RESEARCH_DB_USER, AI_RESEARCH_DB_PASSWORD)")
			os.Exit(1)
		}
		for _, w := range strings.Split(researchWriters, ",") {
			if w = strings.TrimSpace(w); w != "" {
				researchGrants.Writers = append(researchGrants.Writers, w)
			}
		}
		var errQuick, errDeep error
		candidates.Quick, errQuick = research.ParseCandidates(quickCandidates)
		candidates.Deep, errDeep = research.ParseCandidates(deepCandidates)
		if err := errors.Join(errQuick, errDeep); err != nil {
			setupLog.Error(err, "invalid --research-candidates-quick or --research-candidates-deep")
			os.Exit(1)
		}
		if maxUsagePercent < 1 || maxUsagePercent > 100 {
			setupLog.Error(nil, "invalid --research-max-usage-percent, want 1 to 100", "value", maxUsagePercent)
			os.Exit(1)
		}
		researchSetup = research.NewDatabase(owner, researchGrants, clock.RealClock{}, ctrl.Log.WithName("research-db"))
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
		Research:      researchDB.Host != "",
	}
	// The history needs the research database; it records task changes and
	// the comments the project reconciler reads.
	ctx := ctrl.SetupSignalHandler()
	if researchSetup != nil {
		recorder := history.NewRecorder(researchSetup, historyRetention, clock.RealClock{}, ctrl.Log.WithName("history"))
		utilruntime.Must(mgr.Add(recorder))
		if err := history.Watch(ctx, mgr.GetCache(), recorder, time.Now); err != nil {
			setupLog.Error(err, "unable to watch AgentTasks for the history")
			os.Exit(1)
		}
		projects.History = recorder
		researchSetup.SetHistoryRetention(historyRetention)
	}
	if webhookAddr != "" {
		// The bot's own writes cause deliveries too: one sync per project
		// per 5 s, with a trailing one for the last delivery.
		waker := webhook.NewWaker(5*time.Second, 100, clock.RealClock{})
		projects.Wakes = waker.Events()
		projects.Deliveries = webhook.NewDeliveries(clock.RealClock{})
		utilruntime.Must(mgr.Add(&webhook.Server{
			Addr: webhookAddr,
			Handler: &webhook.Receiver{Reader: mgr.GetClient(), Wake: waker.Wake, Deliveries: projects.Deliveries,
				Log: ctrl.Log.WithName("webhook")},
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

	if researchSetup != nil {
		utilruntime.Must(mgr.Add(researchSetup))
		if err := (&controller.AgentResearchReconciler{
			Client:               mgr.GetClient(),
			Scheme:               mgr.GetScheme(),
			APIReader:            mgr.GetAPIReader(),
			DB:                   researchSetup,
			DBConn:               researchDB,
			DefaultAgentImage:    defaultAgentImage,
			DefaultAgentLogLevel: aiv1.LogLevel(agentLogLevel),
			Candidates:           candidates,
			MaxUsagePercent:      int32(maxUsagePercent),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "AgentResearch")
			os.Exit(1)
		}
	}

	utilruntime.Must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	utilruntime.Must(mgr.AddReadyzCheck("readyz", healthz.Ping))

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
