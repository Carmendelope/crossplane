/*
Copyright 2020 The Crossplane Authors.

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

package roles

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/resource/fake"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/crossplane/crossplane/apis/pkg/v1alpha1"
)

func TestReconcile(t *testing.T) {
	errBoom := errors.New("boom")
	now := metav1.Now()

	type args struct {
		mgr  manager.Manager
		opts []ReconcilerOption
	}
	type want struct {
		r   reconcile.Result
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"ProviderRevisionNotFound": {
			reason: "We should not return an error if the ProviderRevision was not found.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(kerrors.NewNotFound(schema.GroupResource{}, "")),
						},
					}),
				},
			},
			want: want{
				r: reconcile.Result{},
			},
		},
		"GetProviderRevisionError": {
			reason: "We should return any other error encountered while getting a ProviderRevision.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(errBoom),
						},
					}),
				},
			},
			want: want{
				err: errors.Wrap(errBoom, errGetPR),
			},
		},
		"ProviderRevisionDeleted": {
			reason: "We should return early if the namespace was deleted.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil, func(o runtime.Object) error {
								d := o.(*v1alpha1.ProviderRevision)
								d.SetDeletionTimestamp(&now)
								return nil
							}),
						},
					}),
				},
			},
			want: want{
				r: reconcile.Result{Requeue: false},
			},
		},
		"ListCRDsError": {
			reason: "We should requeue when an error is encountered listing CRDs.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet:  test.NewMockGetFn(nil),
							MockList: test.NewMockListFn(errBoom),
						},
					}),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"ApplyClusterRoleError": {
			reason: "We should requeue when an error is encountered applying a ClusterRole.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet:  test.NewMockGetFn(nil),
							MockList: test.NewMockListFn(nil),
						},
						Applicator: resource.ApplyFn(func(context.Context, runtime.Object, ...resource.ApplyOption) error {
							return errBoom
						}),
					}),
					WithClusterRoleRenderer(ClusterRoleRenderFn(func(*v1alpha1.ProviderRevision, []v1beta1.CustomResourceDefinition) []rbacv1.ClusterRole {
						return []rbacv1.ClusterRole{{}}
					})),
				},
			},
			want: want{
				r: reconcile.Result{RequeueAfter: shortWait},
			},
		},
		"Successful": {
			reason: "We should not requeue when we successfully apply our ClusterRoles.",
			args: args{
				mgr: &fake.Manager{},
				opts: []ReconcilerOption{
					WithClientApplicator(resource.ClientApplicator{
						Client: &test.MockClient{
							MockGet: test.NewMockGetFn(nil),
							MockList: test.NewMockListFn(nil, func(o runtime.Object) error {
								// Exercise the logic that filters out CRDs that
								// are not owned by the ProviderRevision. Note
								// the CRD's owner's UID matches that of the
								// ProviderRevision because they're both the
								// empty string.
								l := o.(*v1beta1.CustomResourceDefinitionList)
								l.Items = []v1beta1.CustomResourceDefinition{{
									ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{}}},
								}}
								return nil
							}),
						},
						Applicator: resource.ApplyFn(func(context.Context, runtime.Object, ...resource.ApplyOption) error {
							return nil
						}),
					}),
					WithClusterRoleRenderer(ClusterRoleRenderFn(func(*v1alpha1.ProviderRevision, []v1beta1.CustomResourceDefinition) []rbacv1.ClusterRole {
						return []rbacv1.ClusterRole{{}}
					})),
				},
			},
			want: want{
				r: reconcile.Result{Requeue: false},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewReconciler(tc.args.mgr, tc.args.opts...)
			got, err := r.Reconcile(reconcile.Request{})

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nr.Reconcile(...): -want error, +got error:\n%s", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.r, got, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nr.Reconcile(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}
