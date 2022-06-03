package validator

import (
	"context"
	"fmt"

	terrav1alpha1 "github.com/terra-rebels/terra-operator/pkg/apis/terra/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_validator")

// Add creates a new Validator Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileValidator{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("validator-controller", mgr, controller.Options{Reconciler: r})

	if err != nil {
		return err
	}

	// Watch for changes to primary resource Validator
	err = c.Watch(&source.Kind{Type: &terrav1alpha1.Validator{}}, &handler.EnqueueRequestForObject{})

	if err != nil {
		return err
	}

	// Watch for changes to secondary resources and requeue the owner TerradNode
	err = c.Watch(&source.Kind{Type: &terrav1alpha1.TerradNode{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &terrav1alpha1.Validator{},
	})

	if err != nil {
		return err
	}

	err = c.Watch(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &terrav1alpha1.Validator{},
	})

	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileValidator implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileValidator{}

// ReconcileValidator reconciles a Validator object
type ReconcileValidator struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Validator object and makes changes based on the state read
// and what is in the Validator.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileValidator) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Validator")

	// Fetch the Validator instance
	instance := &terrav1alpha1.Validator{}

	err := r.client.Get(context.TODO(), request.NamespacedName, instance)

	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Define a new TerradNode object
	terrad := newTerradNodeForCR(instance)

	// Set Validator instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, terrad, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this TerradNode already exists
	foundTerrad := &terrav1alpha1.TerradNode{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: terrad.Name, Namespace: terrad.Namespace}, foundTerrad)

	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new TerradNode", "TerradNode.Namespace", terrad.Namespace, "TerradNode.Name", terrad.Name)

		//Create TerradNode if it doesnt exist
		err = r.client.Create(context.TODO(), terrad)

		if err != nil {
			// TerradNode creation failed - requeue
			return reconcile.Result{}, err
		}

		// TerradNode created successfully - don't requeue
		return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	if instance.Spec.IsPublic {
		// Define a new Service object
		service := newServiceForCR(instance)

		// Set Validator instance as the owner and controller
		if err := controllerutil.SetControllerReference(instance, service, r.scheme); err != nil {
			return reconcile.Result{}, err
		}

		// Check if this Service already exists
		foundService := &corev1.Service{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)

		if err != nil && errors.IsNotFound(err) {
			reqLogger.Info("Creating a new Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)

			//Create Service if it doesnt exist
			err = r.client.Create(context.TODO(), service)

			if err != nil {
				// Service creation failed - requeue
				return reconcile.Result{}, err
			}

			// Service created successfully - don't requeue
			return reconcile.Result{}, nil
		} else if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func newTerradNodeForCR(cr *terrav1alpha1.Validator) *terrav1alpha1.TerradNode {
	labels := map[string]string{
		"app": cr.Name,
	}

	postStartCommand := fmt.Sprintf(`terrad tx staking create-validator 
		--pubkey=$(terrad tendermint show-validator) 		
		--chain-id=%s
		--moniker="%s" 
		--from=%s
		--amount=%s
		--commission-rate="%s" 
		--commission-max-rate="%s" 
		--commission-max-change-rate="%s" 
		--min-self-delegation="%s"
		--gas auto
		--node tcp://127.0.0.1:26647`,
		cr.Spec.ChainId,
		cr.Spec.Name,
		cr.Spec.FromKeyName,
		cr.Spec.InitialSelfBondAmount,
		cr.Spec.InitialCommissionRate,
		cr.Spec.MaximumCommission,
		cr.Spec.CommissionChangeRate,
		cr.Spec.MinimumSelfBondAmount)

	terrad := &terrav1alpha1.TerradNode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: terrav1alpha1.TerradNodeSpec{
			NodeImage:  cr.Spec.NodeImage,
			IsFullNode: true,
			DataVolume: cr.Spec.DataVolume,
			PostStartCommand: []string{
				postStartCommand,
			},
		},
	}

	return terrad
}

func newServiceForCR(cr *terrav1alpha1.Validator) *corev1.Service {
	labels := map[string]string{
		"app": cr.Name,
	}

	ports := []corev1.ServicePort{
		{
			Name:       "p2p",
			Port:       26656,
			TargetPort: intstr.FromString("p2p"),
		},
		{
			Name:       "rpc",
			Port:       26657,
			TargetPort: intstr.FromString("rpc"),
		},
		{
			Name:       "lcd",
			Port:       1317,
			TargetPort: intstr.FromString("lcd"),
		},
		{
			Name:       "prometheus",
			Port:       26660,
			TargetPort: intstr.FromString("prometheus"),
		},
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: labels,
		},
	}
}
