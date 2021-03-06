/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package core

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"

	fakekubeclient "knative.dev/pkg/client/injection/kube/client/fake"
	fakesecretinformer "knative.dev/pkg/client/injection/kube/informers/core/v1/secret/fake"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/ptr"
	kaccessor "knative.dev/serving/pkg/reconciler/accessor"

	. "knative.dev/pkg/reconciler/testing"
)

var (
	ownerObj = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ownerObj",
			Namespace: "default",
			UID:       "abcd",
		},
	}

	ownerRef = metav1.OwnerReference{
		Kind:       ownerObj.Kind,
		Name:       ownerObj.Name,
		UID:        ownerObj.UID,
		Controller: ptr.Bool(true),
	}

	origin = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "secret",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: map[string][]byte{
			"test-secret": []byte("origin"),
		},
	}

	desired = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "secret",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: map[string][]byte{
			"test-secret": []byte("desired"),
		},
	}

	notOwnedSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"test-secret": []byte("origin"),
		},
	}
)

type FakeAccessor struct {
	client       kubernetes.Interface
	secretLister corev1listers.SecretLister
}

func (f *FakeAccessor) GetKubeClient() kubernetes.Interface {
	return f.client
}

func (f *FakeAccessor) GetSecretLister() corev1listers.SecretLister {
	return f.secretLister
}

func TestReconcileSecretCreate(t *testing.T) {
	ctx, accessor, done := setup([]*corev1.Secret{}, t)
	defer done()
	ReconcileSecret(ctx, ownerObj, desired, accessor)

	secretInformer := fakesecretinformer.Get(ctx)
	if err := wait.PollImmediate(10*time.Millisecond, 3*time.Second, func() (bool, error) {
		secret, err := secretInformer.Lister().Secrets(desired.Namespace).Get(desired.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return cmp.Equal(secret, desired), nil
	}); err != nil {
		t.Fatal("Failed to see secret propagation:", err)
	}
}

func TestReconcileSecretUpdate(t *testing.T) {
	ctx, accessor, done := setup([]*corev1.Secret{origin}, t)
	defer done()

	ReconcileSecret(ctx, ownerObj, desired, accessor)
	secretInformer := fakesecretinformer.Get(ctx)
	if err := wait.PollImmediate(10*time.Millisecond, 3*time.Second, func() (bool, error) {
		secret, err := secretInformer.Lister().Secrets(desired.Namespace).Get(desired.Name)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return cmp.Equal(secret, desired), nil
	}); err != nil {
		t.Fatal("Failed to see secret propagation:", err)
	}
}

func TestNotOwnedFailure(t *testing.T) {
	ctx, accessor, done := setup([]*corev1.Secret{notOwnedSecret}, t)
	defer done()

	_, err := ReconcileSecret(ctx, ownerObj, desired, accessor)
	if err == nil {
		t.Error("Expected to get error when calling ReconcileSecret, but got no error.")
	}
	if !kaccessor.IsNotOwned(err) {
		t.Errorf("Expected to get NotOwnedError but got %v", err)
	}
}

func setup(secrets []*corev1.Secret, t *testing.T) (context.Context, *FakeAccessor, func()) {
	ctx, cancel, _ := SetupFakeContextWithCancel(t)
	secretInformer := fakesecretinformer.Get(ctx)

	fake := fakekubeclient.Get(ctx)
	for _, secret := range secrets {
		fake.CoreV1().Secrets(secret.Namespace).Create(secret)
		secretInformer.Informer().GetIndexer().Add(secret)
	}

	waitInformers, err := controller.RunInformers(ctx.Done(), secretInformer.Informer())
	if err != nil {
		t.Fatal("Failed to start secret informer:", err)
	}

	return ctx, &FakeAccessor{
			client:       fake,
			secretLister: secretInformer.Lister(),
		}, func() {
			cancel()
			waitInformers()
		}
}
