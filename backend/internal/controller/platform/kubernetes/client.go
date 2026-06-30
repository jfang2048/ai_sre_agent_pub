package kubernetes

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes client
type Client struct {
	clientset *kubernetes.Clientset
	logger    *zap.Logger
	namespace string
}

// Config configures the Kubernetes client
type Config struct {
	Kubeconfig string
	Namespace  string
	InCluster  bool
	Timeout    time.Duration
}

// NewClient creates a new Kubernetes client
func NewClient(config Config, logger *zap.Logger) (*Client, error) {
	var err error
	var restConfig *rest.Config

	if config.InCluster {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config failed: %w", err)
		}
	} else {
		restConfig, err = clientcmd.BuildConfigFromFlags("", config.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig failed: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("client creation failed: %w", err)
	}

	ns := config.Namespace
	if ns == "" {
		ns = "default"
	}

	return &Client{
		clientset: clientset,
		logger:    logger.With(zap.String("component", "k8s_client")),
		namespace: ns,
	}, nil
}

// ScaleDeployment scales a deployment
func (c *Client) ScaleDeployment(ctx context.Context, name string, replicas int32) error {
	c.logger.Info("scaling deployment",
		zap.String("deployment", name),
		zap.Int32("replicas", replicas))

	deployments := c.clientset.AppsV1().Deployments(c.namespace)
	scale, err := deployments.GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale: %w", err)
	}

	scale.Spec.Replicas = replicas

	_, err = deployments.UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update scale: %w", err)
	}

	return nil
}

// RestartPod restarts a pod by deleting it
func (c *Client) RestartPod(ctx context.Context, name string) error {
	c.logger.Info("restarting pod", zap.String("pod", name))

	pods := c.clientset.CoreV1().Pods(c.namespace)
	err := pods.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	return nil
}

// GetPods returns all pods in the namespace
func (c *Client) GetPods(ctx context.Context) ([]string, error) {
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	names := make([]string, len(pods.Items))
	for i, pod := range pods.Items {
		names[i] = pod.Name
	}

	return names, nil
}

// GetDeploymentStatus gets deployment status
func (c *Client) GetDeploymentStatus(ctx context.Context, name string) (*DeploymentStatus, error) {
	deploy, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	return &DeploymentStatus{
		Name:              deploy.Name,
		Replicas:          *deploy.Spec.Replicas,
		ReadyReplicas:     deploy.Status.ReadyReplicas,
		UpdatedReplicas:   deploy.Status.UpdatedReplicas,
		AvailableReplicas: deploy.Status.AvailableReplicas,
	}, nil
}

// DeploymentStatus represents deployment status
type DeploymentStatus struct {
	Name              string
	Replicas          int32
	ReadyReplicas     int32
	UpdatedReplicas   int32
	AvailableReplicas int32
}

// Scaler handles HPA and VPA operations
type Scaler struct {
	client *Client
	logger *zap.Logger
}

// NewScaler creates a new scaler
func NewScaler(client *Client, logger *zap.Logger) *Scaler {
	return &Scaler{
		client: client,
		logger: logger.With(zap.String("component", "scaler")),
	}
}

// ScaleUp scales up a deployment
func (s *Scaler) ScaleUp(ctx context.Context, deployment string, increment int32) error {
	status, err := s.client.GetDeploymentStatus(ctx, deployment)
	if err != nil {
		return err
	}

	newReplicas := status.Replicas + increment
	return s.client.ScaleDeployment(ctx, deployment, newReplicas)
}

// ScaleDown scales down a deployment
func (s *Scaler) ScaleDown(ctx context.Context, deployment string, decrement int32) error {
	status, err := s.client.GetDeploymentStatus(ctx, deployment)
	if err != nil {
		return err
	}

	newReplicas := status.Replicas - decrement
	if newReplicas < 1 {
		newReplicas = 1
	}

	return s.client.ScaleDeployment(ctx, deployment, newReplicas)
}

// SetReplicas sets the exact number of replicas
func (s *Scaler) SetReplicas(ctx context.Context, deployment string, replicas int32) error {
	return s.client.ScaleDeployment(ctx, deployment, replicas)
}

// PodWatcher watches pod events
type PodWatcher struct {
	client *Client
	logger *zap.Logger
}

// NewPodWatcher creates a new pod watcher
func NewPodWatcher(client *Client, logger *zap.Logger) *PodWatcher {
	return &PodWatcher{
		client: client,
		logger: logger.With(zap.String("component", "pod_watcher")),
	}
}

// WatchPods watches pod events
func (w *PodWatcher) WatchPods(ctx context.Context, handler PodEventHandler) error {
	watch, err := w.client.clientset.CoreV1().Pods(w.client.namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch pods: %w", err)
	}

	defer watch.Stop()

	for event := range watch.ResultChan() {
		eventType := string(event.Type)
		switch eventType {
		case "ADDED", "MODIFIED", "DELETED":
			handler(event.Object, eventType)
		}
	}

	return nil
}

// PodEventHandler handles pod events
type PodEventHandler func(obj interface{}, eventType string)

// EventsWatcher watches Kubernetes events
type EventsWatcher struct {
	client *Client
	logger *zap.Logger
}

// NewEventsWatcher creates a new events watcher
func NewEventsWatcher(client *Client, logger *zap.Logger) *EventsWatcher {
	return &EventsWatcher{
		client: client,
		logger: logger.With(zap.String("component", "events_watcher")),
	}
}

// WatchEvents watches Kubernetes events
func (w *EventsWatcher) WatchEvents(ctx context.Context, handler EventHandler) error {
	watch, err := w.client.clientset.CoreV1().Events(w.client.namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}

	defer watch.Stop()

	for event := range watch.ResultChan() {
		handler(event.Object)
	}

	return nil
}

// EventHandler handles events
type EventHandler func(obj interface{})
