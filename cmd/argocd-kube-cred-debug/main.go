package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/db"
	"github.com/argoproj/argo-cd/v2/util/env"
	"github.com/argoproj/argo-cd/v2/util/kube"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

func main() {
	secretName := flag.String("argocd-secret-name", env.StringFromEnv("ARGOCD_SECRET_NAME", ""), "the argocd secret name for a given remote cluster")
	secretNamespace := flag.String("argocd-secret-namespace", env.StringFromEnv("ARGOCD_SECRET_NAMESPACE", ""), "the namespace where the secret lives")
	kubeConfigPath := flag.String("kubeconfig-path", env.StringFromEnv("KUBE_CONFIG_PATH", ""), "the path to the kubeconfig file for out of cluster connection")
	flag.Parse()

	if secretName == nil || *secretName == "" {
		log.Fatal("env var ARGOCD_SECRET_NAME must be provided")
	}
	if secretNamespace == nil || *secretNamespace == "" {
		log.Fatal("env var ARGOCD_SECRET_NAMESPACE must be provided")
	}

	fmt.Println("Environment Variables:")
	for _, env := range os.Environ() {
		fmt.Println(env)
	}

	var restConfig *rest.Config
	var err error
	if kubeConfigPath != nil && *kubeConfigPath != "" {
		restConfig, err = clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
		if err != nil {
			log.Fatalf("error building rest config: %s", err)
		}
	} else {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			log.Fatalf("error getting incluster config: %s", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("error creating k8s clientset: %s", err)
	}

	clusterSecret, err := clientset.CoreV1().Secrets(*secretNamespace).Get(context.Background(), *secretName, v1.GetOptions{})
	if err != nil {
		log.Fatalf("error getting secret: %s", err)
	}

	c, err := db.SecretToCluster(clusterSecret)
	if err != nil {
		log.Fatalf("error converting secret to cluster: %s", err)
	}
	// c.Config.AWSAuthConfig.RoleARN = "arn:aws:iam::795188202216:role/k8s-dev-argocd-usw2-dev"

	remoteK8sConfig := toRemoteConfig(c)

	kubectl := kube.NewKubectl()
	version, err := kubectl.GetServerVersion(remoteK8sConfig)
	if err != nil {
		log.Fatalf("error getting server version: %s", err)
	}

	fmt.Printf("cluster: %s version: %s\n", string(clusterSecret.Data["name"]), version)
}

func toRemoteConfig(c *v1alpha1.Cluster) *rest.Config {
	config := RawRestConfig(c)
	err := v1alpha1.SetK8SConfigDefaults(config)
	if err != nil {
		panic(fmt.Sprintf("Unable to apply K8s REST config defaults: %v", err))
	}
	return config
}

func RawRestConfig(c *v1alpha1.Cluster) *rest.Config {
	var config *rest.Config
	var err error
	if c.Server == v1alpha1.KubernetesInternalAPIServerAddr && env.ParseBoolFromEnv(v1alpha1.EnvVarFakeInClusterConfig, false) {
		conf, exists := os.LookupEnv("KUBECONFIG")
		if exists {
			config, err = clientcmd.BuildConfigFromFlags("", conf)
		} else {
			var homeDir string
			homeDir, err = os.UserHomeDir()
			if err != nil {
				homeDir = ""
			}
			config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(homeDir, ".kube", "config"))
		}
	} else if c.Server == v1alpha1.KubernetesInternalAPIServerAddr && c.Config.Username == "" && c.Config.Password == "" && c.Config.BearerToken == "" {
		config, err = rest.InClusterConfig()
	} else if c.Server == v1alpha1.KubernetesInternalAPIServerAddr {
		config, err = rest.InClusterConfig()
		if err == nil {
			config.Username = c.Config.Username
			config.Password = c.Config.Password
			config.BearerToken = c.Config.BearerToken
			config.BearerTokenFile = ""
		}
	} else {
		tlsClientConfig := rest.TLSClientConfig{
			Insecure:   c.Config.TLSClientConfig.Insecure,
			ServerName: c.Config.TLSClientConfig.ServerName,
			CertData:   c.Config.TLSClientConfig.CertData,
			KeyData:    c.Config.TLSClientConfig.KeyData,
			CAData:     c.Config.TLSClientConfig.CAData,
		}
		if c.Config.AWSAuthConfig != nil {
			args := []string{fmt.Sprintf("--cluster-name=%s", c.Config.AWSAuthConfig.ClusterName)}
			if c.Config.AWSAuthConfig.RoleARN != "" {
				args = append(args, fmt.Sprintf("--role-arn=%s", c.Config.AWSAuthConfig.RoleARN))
			}
			config = &rest.Config{
				Host:            c.Server,
				TLSClientConfig: tlsClientConfig,
				ExecProvider: &api.ExecConfig{
					APIVersion:      "client.authentication.k8s.io/v1beta1",
					Command:         "argocd-k8s-auth",
					Args:            args,
					InteractiveMode: api.NeverExecInteractiveMode,
				},
			}
		} else if c.Config.ExecProviderConfig != nil {
			var env []api.ExecEnvVar
			if c.Config.ExecProviderConfig.Env != nil {
				for key, value := range c.Config.ExecProviderConfig.Env {
					env = append(env, api.ExecEnvVar{
						Name:  key,
						Value: value,
					})
				}
			}
			config = &rest.Config{
				Host:            c.Server,
				TLSClientConfig: tlsClientConfig,
				ExecProvider: &api.ExecConfig{
					APIVersion:      c.Config.ExecProviderConfig.APIVersion,
					Command:         c.Config.ExecProviderConfig.Command,
					Args:            c.Config.ExecProviderConfig.Args,
					Env:             env,
					InstallHint:     c.Config.ExecProviderConfig.InstallHint,
					InteractiveMode: api.NeverExecInteractiveMode,
				},
			}
		} else {
			config = &rest.Config{
				Host:            c.Server,
				Username:        c.Config.Username,
				Password:        c.Config.Password,
				BearerToken:     c.Config.BearerToken,
				TLSClientConfig: tlsClientConfig,
			}
		}
	}
	if err != nil {
		panic(fmt.Sprintf("Unable to create K8s REST config: %v", err))
	}
	config.Timeout = v1alpha1.K8sServerSideTimeout
	config.QPS = v1alpha1.K8sClientConfigQPS
	config.Burst = v1alpha1.K8sClientConfigBurst
	return config
}
