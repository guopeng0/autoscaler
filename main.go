/*
Copyright 2020 The KEDA Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kedav1alpha1 "github.com/kedacore/keda/v2/api/v1alpha1"
	"github.com/kedacore/keda/v2/controllers"
	"github.com/kedacore/keda/v2/version"
	// +kubebuilder:scaffold:imports
)

var (
	scheme = apimachineryruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(kedav1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// getWatchNamespace returns the namespace the operator should be watching for changes
func getWatchNamespace() (string, error) {
	const WatchNamespaceEnvVar = "WATCH_NAMESPACE"
	ns, found := os.LookupEnv(WatchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", WatchNamespaceEnvVar)
	}
	return ns, nil
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	// Add the zap logger flag set to the CLI.
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	namespace, err := getWatchNamespace()
	if err != nil {
		setupLog.Error(err, "failed to get watch namespace")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: ":8081",
		Port:                   9443,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "operator.keda.sh",
		Namespace:              namespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Add readiness probe
	err = mgr.AddReadyzCheck("ready-ping", healthz.Ping)
	if err != nil {
		setupLog.Error(err, "Unable to add a readiness check")
		os.Exit(1)
	}

	// Add liveness probe
	err = mgr.AddHealthzCheck("health-ping", healthz.Ping)
	if err != nil {
		setupLog.Error(err, "Unable to add a health check")
		os.Exit(1)
	}

	globalHTTPTimeoutStr := os.Getenv("KEDA_HTTP_DEFAULT_TIMEOUT")
	if globalHTTPTimeoutStr == "" {
		// default to 3 seconds if they don't pass the env var
		globalHTTPTimeoutStr = "3000"
	}

	globalHTTPTimeoutMS, err := strconv.Atoi(globalHTTPTimeoutStr)
	if err != nil {
		setupLog.Error(err, "Invalid KEDA_HTTP_DEFAULT_TIMEOUT")
		return
	}

	globalHTTPTimeout := time.Duration(globalHTTPTimeoutMS) * time.Millisecond
	eventRecorder := mgr.GetEventRecorderFor("keda-operator")

	if err = (&controllers.ScaledObjectReconciler{
		Client:            mgr.GetClient(),
		Log:               ctrl.Log.WithName("controllers").WithName("ScaledObject"),
		Scheme:            mgr.GetScheme(),
		GlobalHTTPTimeout: globalHTTPTimeout,
		Recorder:          eventRecorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ScaledObject")
		os.Exit(1)
	}
	if err = (&controllers.ScaledJobReconciler{
		Client:            mgr.GetClient(),
		Log:               ctrl.Log.WithName("controllers").WithName("ScaledJob"),
		Scheme:            mgr.GetScheme(),
		GlobalHTTPTimeout: globalHTTPTimeout,
		Recorder:          eventRecorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ScaledJob")
		os.Exit(1)
	}
	if err = (&controllers.TriggerAuthenticationReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("TriggerAuthentication"),
		Recorder: eventRecorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TriggerAuthentication")
		os.Exit(1)
	}
	if err = (&controllers.ClusterTriggerAuthenticationReconciler{
		Client:   mgr.GetClient(),
		Log:      ctrl.Log.WithName("controllers").WithName("ClusterTriggerAuthentication"),
		Recorder: eventRecorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterTriggerAuthentication")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("Starting manager")
	setupLog.Info(fmt.Sprintf("KEDA Version: %s", version.Version))
	setupLog.Info(fmt.Sprintf("Git Commit: %s", version.GitCommit))
	setupLog.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	setupLog.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}