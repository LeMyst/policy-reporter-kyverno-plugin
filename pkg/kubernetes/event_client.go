package kubernetes

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kyverno/policy-reporter-kyverno-plugin/pkg/kyverno"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type eventClient struct {
	publisher      kyverno.ViolationPublisher
	factory        informers.SharedInformerFactory
	policyStore    *kyverno.PolicyStore
	startUp        time.Time
	eventNamespace string
}

func (e *eventClient) Run(stopper chan struct{}) error {
	informer := e.factory.Core().V1().Events().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if event, ok := obj.(*corev1.Event); ok {
				if !strings.Contains(event.Message, "(blocked)") || e.startUp.After(event.CreationTimestamp.Time) {
					return
				}

				policy, ok := e.policyStore.Get(string(event.InvolvedObject.UID))
				if !ok {
					log.Printf("[ERROR] policy not found %s\n", event.InvolvedObject.Name)
					return
				}

				e.publisher.Publish(ConvertEvent(event, policy, false))
			}
		},
		UpdateFunc: func(old interface{}, obj interface{}) {
			if event, ok := obj.(*corev1.Event); ok {
				if !strings.Contains(event.Message, "(blocked)") || e.startUp.After(event.LastTimestamp.Time) {
					return
				}

				policy, ok := e.policyStore.Get(string(event.InvolvedObject.UID))
				if !ok {
					log.Printf("[ERROR] policy not found %s\n", event.InvolvedObject.Name)
					return
				}

				e.publisher.Publish(ConvertEvent(event, policy, true))
			}
		},
	})

	e.factory.Start(stopper)

	if !cache.WaitForCacheSync(stopper, informer.HasSynced) {
		return fmt.Errorf("failed to sync events")
	}

	return nil
}

func ConvertEvent(event *corev1.Event, policy kyverno.Policy, updated bool) kyverno.PolicyViolation {
	parts := strings.Split(event.Message, " ")
	resourceParts := strings.Split(parts[1][0:len(parts[1])-1], "/")

	var namespace, name string

	if len(resourceParts) == 2 {
		namespace = strings.TrimSpace(resourceParts[0])
		name = strings.TrimSpace(resourceParts[1])
	} else {
		name = strings.TrimSpace(resourceParts[0])
	}

	ruleName := strings.TrimSpace(parts[2][1 : len(parts[2])-1])

	message := event.Message
	for _, rule := range policy.Rules {
		if rule.Name == ruleName {
			message = policy.Rules[0].ValidateMessage
		}
	}

	return kyverno.PolicyViolation{
		Resource: kyverno.ViolationResource{
			Kind:      strings.TrimSpace(parts[0]),
			Namespace: namespace,
			Name:      name,
		},
		Policy: kyverno.ViolationPolicy{
			Name:     policy.Name,
			Rule:     ruleName,
			Message:  message,
			Category: policy.Category,
			Severity: policy.Severity,
		},
		Timestamp: event.LastTimestamp.Time,
		Updated:   updated,
		Event: kyverno.ViolationEvent{
			Name: event.Name,
			UID:  string(event.UID),
		},
	}
}

func NewEventClient(client k8s.Interface, publisher kyverno.ViolationPublisher, policyStore *kyverno.PolicyStore, eventNamespace string) kyverno.EventClient {
	factory := informers.NewFilteredSharedInformerFactory(client, 0, eventNamespace, func(lo *v1.ListOptions) {
		lo.FieldSelector = fields.Set{
			"source": "kyverno-admission",
			"reason": "PolicyViolation",
			"type":   "Warning",
		}.AsSelector().String()
	})

	return &eventClient{
		publisher:   publisher,
		factory:     factory,
		policyStore: policyStore,
		startUp:     time.Now(),
	}
}
