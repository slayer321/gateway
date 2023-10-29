// Copyright Envoy Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build e2e
// +build e2e

package tests

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/gateway-api/conformance/utils/config"
	"sigs.k8s.io/gateway-api/conformance/utils/http"
	"sigs.k8s.io/gateway-api/conformance/utils/kubernetes"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func init() {
	ConformanceTests = append(ConformanceTests, TCPRouteTest)
}

// GatewayRef is a tiny type for specifying an TCP Route ParentRef without
// relying on a specific api version.
type GatewayRef struct {
	types.NamespacedName
	listenerNames []*gatewayv1.SectionName
}

// NewGatewayRef creates a GatewayRef resource.  ListenerNames are optional.
func NewGatewayRef(nn types.NamespacedName, listenerNames ...string) GatewayRef {
	var listeners []*gatewayv1.SectionName

	if len(listenerNames) == 0 {
		listenerNames = append(listenerNames, "")
	}

	for _, listener := range listenerNames {
		sectionName := gatewayv1.SectionName(listener)
		listeners = append(listeners, &sectionName)
	}
	return GatewayRef{
		NamespacedName: nn,
		listenerNames:  listeners,
	}
}

var TCPRouteTest = suite.ConformanceTest{
	ShortName:   "TCPRoute",
	Description: "Testing TCP Route",
	Manifests:   []string{"testdata/tcp-route.yaml"},
	Test: func(t *testing.T, suite *suite.ConformanceTestSuite) {
		t.Run("tcp-route-1", func(t *testing.T) {
			ns := "gateway-conformance-infra"
			routeNN := types.NamespacedName{Name: "tcp-app-1", Namespace: ns}
			gwNN := types.NamespacedName{Name: "my-tcp-gateway", Namespace: ns}
			gwAddr := GatewayAndTCPRoutesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, suite.ControllerName, NewGatewayRef(gwNN), routeNN)
			OkResp := http.ExpectedResponse{
				Request: http.Request{
					Path: "/",
				},
				Response: http.Response{
					StatusCode: 200,
				},
				Namespace: ns,
			}

			// Send a request to an valid path and expect a successful response
			http.MakeRequestAndExpectEventuallyConsistentResponse(t, suite.RoundTripper, suite.TimeoutConfig, gwAddr, OkResp)
		})
		t.Run("tcp-route-2", func(t *testing.T) {
			ns := "gateway-conformance-infra"
			routeNN := types.NamespacedName{Name: "tcp-app-2", Namespace: ns}
			gwNN := types.NamespacedName{Name: "my-tcp-gateway", Namespace: ns}
			gwAddr := GatewayAndTCPRoutesMustBeAccepted(t, suite.Client, suite.TimeoutConfig, suite.ControllerName, NewGatewayRef(gwNN), routeNN)
			OkResp := http.ExpectedResponse{
				Request: http.Request{
					Path: "/",
				},
				Response: http.Response{
					StatusCode: 200,
				},
				Namespace: ns,
			}

			// Send a request to an valid path and expect a successful response
			http.MakeRequestAndExpectEventuallyConsistentResponse(t, suite.RoundTripper, suite.TimeoutConfig, gwAddr, OkResp)
		})

	},
}

func GatewayAndTCPRoutesMustBeAccepted(t *testing.T, c client.Client, timeoutConfig config.TimeoutConfig, controllerName string, gw GatewayRef, routeNNs ...types.NamespacedName) string {
	t.Helper()

	gwAddr, err := kubernetes.WaitForGatewayAddress(t, c, timeoutConfig, gw.NamespacedName)
	require.NoErrorf(t, err, "timed out waiting for Gateway address to be assigned")

	ns := gatewayv1.Namespace(gw.Namespace)
	kind := gatewayv1.Kind("Gateway")

	for _, routeNN := range routeNNs {
		namespaceRequired := true
		if routeNN.Namespace == gw.Namespace {
			namespaceRequired = false
		}

		var parents []gatewayv1.RouteParentStatus
		for _, listener := range gw.listenerNames {
			parents = append(parents, gatewayv1.RouteParentStatus{
				ParentRef: gatewayv1.ParentReference{
					Group:       (*gatewayv1.Group)(&gatewayv1.GroupVersion.Group),
					Kind:        &kind,
					Name:        gatewayv1.ObjectName(gw.Name),
					Namespace:   &ns,
					SectionName: listener,
				},
				ControllerName: gatewayv1.GatewayController(controllerName),
				Conditions: []metav1.Condition{{
					Type:   string(gatewayv1.RouteConditionAccepted),
					Status: metav1.ConditionTrue,
					Reason: string(gatewayv1.RouteReasonAccepted),
				}},
			})
		}
		TCPRouteMustHaveParents(t, c, timeoutConfig, routeNN, parents, namespaceRequired)
	}

	return gwAddr

}

func TCPRouteMustHaveParents(t *testing.T, client client.Client, timeoutConfig config.TimeoutConfig, routeName types.NamespacedName, parents []gatewayv1.RouteParentStatus, namespaceRequired bool) {
	t.Helper()

	var actual []gatewayv1.RouteParentStatus
	waitErr := wait.PollUntilContextTimeout(context.Background(), 1*time.Second, timeoutConfig.RouteMustHaveParents, true, func(ctx context.Context) (bool, error) {
		route := &v1alpha2.TCPRoute{}
		err := client.Get(ctx, routeName, route)
		if err != nil {
			return false, fmt.Errorf("error fetching HTTPRoute: %w", err)
		}

		actual = route.Status.Parents
		return parentsForRouteMatch(t, routeName, parents, actual, namespaceRequired), nil
	})
	require.NoErrorf(t, waitErr, "error waiting for TCPRoute to have parents matching expectations")
}

func parentsForRouteMatch(t *testing.T, routeName types.NamespacedName, expected, actual []gatewayv1.RouteParentStatus, namespaceRequired bool) bool {
	t.Helper()

	if len(expected) != len(actual) {
		t.Logf("Route %s/%s expected %d Parents got %d", routeName.Namespace, routeName.Name, len(expected), len(actual))
		return false
	}

	// TODO(robscott): Allow for arbitrarily ordered parents
	for i, eParent := range expected {
		aParent := actual[i]
		if aParent.ControllerName != eParent.ControllerName {
			t.Logf("Route %s/%s ControllerName doesn't match", routeName.Namespace, routeName.Name)
			return false
		}
		if !reflect.DeepEqual(aParent.ParentRef.Group, eParent.ParentRef.Group) {
			t.Logf("Route %s/%s expected ParentReference.Group to be %v, got %v", routeName.Namespace, routeName.Name, eParent.ParentRef.Group, aParent.ParentRef.Group)
			return false
		}
		if !reflect.DeepEqual(aParent.ParentRef.Kind, eParent.ParentRef.Kind) {
			t.Logf("Route %s/%s expected ParentReference.Kind to be %v, got %v", routeName.Namespace, routeName.Name, eParent.ParentRef.Kind, aParent.ParentRef.Kind)
			return false
		}
		if aParent.ParentRef.Name != eParent.ParentRef.Name {
			t.Logf("Route %s/%s ParentReference.Name doesn't match", routeName.Namespace, routeName.Name)
			return false
		}
		if !reflect.DeepEqual(aParent.ParentRef.Namespace, eParent.ParentRef.Namespace) {
			if namespaceRequired || aParent.ParentRef.Namespace != nil {
				t.Logf("Route %s/%s expected ParentReference.Namespace to be %v, got %v", routeName.Namespace, routeName.Name, eParent.ParentRef.Namespace, aParent.ParentRef.Namespace)
				return false
			}
		}
		if !conditionsMatch(t, eParent.Conditions, aParent.Conditions) {
			return false
		}
	}

	t.Logf("Route %s/%s Parents matched expectations", routeName.Namespace, routeName.Name)
	return true
}

func conditionsMatch(t *testing.T, expected, actual []metav1.Condition) bool {
	t.Helper()

	if len(actual) < len(expected) {
		t.Logf("Expected more conditions to be present")
		return false
	}
	for _, condition := range expected {
		if !findConditionInList(t, actual, condition.Type, string(condition.Status), condition.Reason) {
			return false
		}
	}

	t.Logf("Conditions matched expectations")
	return true
}

// findConditionInList finds a condition in a list of Conditions, checking
// the Name, Value, and Reason. If an empty reason is passed, any Reason will match.
// If an empty status is passed, any Status will match.
func findConditionInList(t *testing.T, conditions []metav1.Condition, condName, expectedStatus, expectedReason string) bool {
	t.Helper()

	for _, cond := range conditions {
		if cond.Type == condName {
			// an empty Status string means "Match any status".
			if expectedStatus == "" || cond.Status == metav1.ConditionStatus(expectedStatus) {
				// an empty Reason string means "Match any reason".
				if expectedReason == "" || cond.Reason == expectedReason {
					return true
				}
				t.Logf("%s condition Reason set to %s, expected %s", condName, cond.Reason, expectedReason)
			}

			t.Logf("%s condition set to Status %s with Reason %v, expected Status %s", condName, cond.Status, cond.Reason, expectedStatus)
		}
	}

	t.Logf("%s was not in conditions list [%v]", condName, conditions)
	return false
}
