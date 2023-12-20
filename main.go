package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/argoproj/argo-cd/v2/util/db"
	"github.com/argoproj/argo-cd/v2/util/kube"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	secretName := os.Getenv("ARGOCD_SECRET_NAME")
	if secretName == "" {
		log.Fatal("env var ARGOCD_SECRET_NAME must be provided")
	}
	secretNamespace := os.Getenv("ARGOCD_SECRET_NAMESPACE")
	if secretName == "" {
		log.Fatal("env var ARGOCD_SECRET_NAMESPACE must be provided")
	}

	kubeConfigPath := os.Getenv("KUBE_CONFIG_PATH")
	var restConfig *rest.Config
	var err error
	if kubeConfigPath != "" {
		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
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

	clusterSecret, err := clientset.CoreV1().Secrets(secretNamespace).Get(context.Background(), secretName, v1.GetOptions{})
	if err != nil {
		log.Fatalf("error getting secret: %s", err)
	}

	c, err := db.SecretToCluster(clusterSecret)
	if err != nil {
		log.Fatalf("error converting secret to cluster: %s", err)
	}

	remoteK8sConfig := c.RESTConfig()

	kubectl := kube.NewKubectl()
	version, err := kubectl.GetServerVersion(remoteK8sConfig)
	if err != nil {
		log.Fatalf("error getting server version: %s", err)
	}

	fmt.Printf("cluster: %s version: %s\n", string(clusterSecret.Data["name"]), version)
}
