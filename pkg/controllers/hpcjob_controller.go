package controllers

import (
	"context"
	"fmt"
	"time"

	ailsv1alpha1 "ails-hpc/api/v1alpha1"

	commonv1 "github.com/kubeflow/common/pkg/apis/common/v1"
	mpiv2beta1 "github.com/kubeflow/mpi-operator/pkg/apis/kubeflow/v2beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type HpcJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *HpcJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var hpcJob ailsv1alpha1.HpcJob
	if err := r.Get(ctx, req.NamespacedName, &hpcJob); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch HpcJob")
		return ctrl.Result{}, err
	}

	queueName := hpcJob.Spec.Queue
	if queueName == "" {
		queueName = "user-queue"
	}

	// 1. Ensure Local-Path Storage PVC if requested
	if hpcJob.Spec.StorageSize != "" {
		pvcName := fmt.Sprintf("%s-pvc", hpcJob.Name)
		var existingPVC corev1.PersistentVolumeClaim
		err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: hpcJob.Namespace}, &existingPVC)
		if errors.IsNotFound(err) {
			newPVC := r.constructPVC(&hpcJob, pvcName)
			if err := controllerutil.SetControllerReference(&hpcJob, newPVC, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("Creating Local-Path Storage PVC", "PVC.Name", pvcName, "Size", hpcJob.Spec.StorageSize)
			if err := r.Create(ctx, newPVC); err != nil {
				logger.Error(err, "Failed to create PVC")
				return ctrl.Result{}, err
			}
		}
	}

	// 2. Dispatch based on JobType (Default: "mpi", Option: "batch")
	if hpcJob.Spec.JobType == "batch" {
		return r.reconcileBatchJob(ctx, &hpcJob, queueName)
	}
	return r.reconcileMPIJob(ctx, &hpcJob, queueName)
}

func (r *HpcJobReconciler) reconcileMPIJob(ctx context.Context, hpcJob *ailsv1alpha1.HpcJob, queueName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var mpiJob mpiv2beta1.MPIJob
	err := r.Get(ctx, types.NamespacedName{Name: hpcJob.Name, Namespace: hpcJob.Namespace}, &mpiJob)
	if errors.IsNotFound(err) {
		newMPIJob := r.constructMPIJob(hpcJob, queueName)
		if err := controllerutil.SetControllerReference(hpcJob, newMPIJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Creating child MPIJob for HpcJob", "MPIJob.Name", newMPIJob.Name, "Queue", queueName)
		if err := r.Create(ctx, newMPIJob); err != nil {
			logger.Error(err, "Failed to create child MPIJob")
			return ctrl.Result{}, err
		}

		hpcJob.Status.Phase = "Pending"
		if err := r.Status().Update(ctx, hpcJob); err != nil {
			logger.Error(err, "Failed to update HpcJob status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	newPhase := "Pending"
	if mpiJob.Status.Conditions != nil && len(mpiJob.Status.Conditions) > 0 {
		lastCond := mpiJob.Status.Conditions[len(mpiJob.Status.Conditions)-1]
		switch string(lastCond.Type) {
		case "Created", "Running":
			newPhase = "Running"
			if hpcJob.Status.StartTime == nil {
				now := metav1.Now()
				hpcJob.Status.StartTime = &now
			}
		case "Succeeded":
			newPhase = "Succeeded"
			if hpcJob.Status.CompletionTime == nil {
				now := metav1.Now()
				hpcJob.Status.CompletionTime = &now
			}
		case "Failed":
			newPhase = "Failed"
			if hpcJob.Status.CompletionTime == nil {
				now := metav1.Now()
				hpcJob.Status.CompletionTime = &now
			}
		}
	}

	r.calculateAccounting(hpcJob)

	if hpcJob.Status.Phase != newPhase {
		hpcJob.Status.Phase = newPhase
		if err := r.Status().Update(ctx, hpcJob); err != nil {
			logger.Error(err, "Failed to update HpcJob status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *HpcJobReconciler) reconcileBatchJob(ctx context.Context, hpcJob *ailsv1alpha1.HpcJob, queueName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var k8sJob batchv1.Job
	err := r.Get(ctx, types.NamespacedName{Name: hpcJob.Name, Namespace: hpcJob.Namespace}, &k8sJob)
	if errors.IsNotFound(err) {
		newBatchJob := r.constructBatchJob(hpcJob, queueName)
		if err := controllerutil.SetControllerReference(hpcJob, newBatchJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Creating child Batch Job for HpcJob", "Job.Name", newBatchJob.Name, "Queue", queueName)
		if err := r.Create(ctx, newBatchJob); err != nil {
			logger.Error(err, "Failed to create child Batch Job")
			return ctrl.Result{}, err
		}

		hpcJob.Status.Phase = "Pending"
		if err := r.Status().Update(ctx, hpcJob); err != nil {
			logger.Error(err, "Failed to update HpcJob status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	newPhase := "Pending"
	if k8sJob.Status.Active > 0 {
		newPhase = "Running"
		if hpcJob.Status.StartTime == nil {
			now := metav1.Now()
			hpcJob.Status.StartTime = &now
		}
	} else if k8sJob.Status.Succeeded > 0 {
		newPhase = "Succeeded"
		if hpcJob.Status.CompletionTime == nil {
			now := metav1.Now()
			hpcJob.Status.CompletionTime = &now
		}
	} else if k8sJob.Status.Failed > 0 {
		newPhase = "Failed"
		if hpcJob.Status.CompletionTime == nil {
			now := metav1.Now()
			hpcJob.Status.CompletionTime = &now
		}
	}

	r.calculateAccounting(hpcJob)

	if hpcJob.Status.Phase != newPhase {
		hpcJob.Status.Phase = newPhase
		if err := r.Status().Update(ctx, hpcJob); err != nil {
			logger.Error(err, "Failed to update HpcJob status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *HpcJobReconciler) calculateAccounting(hpcJob *ailsv1alpha1.HpcJob) {
	if hpcJob.Status.StartTime != nil && hpcJob.Status.CompletionTime != nil {
		duration := hpcJob.Status.CompletionTime.Sub(hpcJob.Status.StartTime.Time)
		hpcJob.Status.ExecutionDuration = duration.Round(time.Second).String()
		hpcJob.Status.CoreHours = float64(hpcJob.Spec.Slots) * (duration.Hours())
	}
}

func (r *HpcJobReconciler) constructPVC(hpcJob *ailsv1alpha1.HpcJob, pvcName string) *corev1.PersistentVolumeClaim {
	storageClassName := "local-path"
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: hpcJob.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(hpcJob.Spec.StorageSize),
				},
			},
		},
	}
}

func (r *HpcJobReconciler) constructBatchJob(hpcJob *ailsv1alpha1.HpcJob, queueName string) *batchv1.Job {
	labels := map[string]string{
		"kueue.x-k8s.io/queue-name":    queueName,
		"app.kubernetes.io/created-by": "hpc-controller",
	}

	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	if hpcJob.Spec.StorageSize != "" {
		pvcName := fmt.Sprintf("%s-pvc", hpcJob.Name)
		volumes = append(volumes, corev1.Volume{
			Name: "shared-workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "shared-workspace",
			MountPath: "/workspace",
		})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hpcJob.Name,
			Namespace: hpcJob.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes:       volumes,
					Containers: []corev1.Container{
						{
							Name:         "batch-worker",
							Image:        hpcJob.Spec.Image,
							Command:      hpcJob.Spec.Command,
							VolumeMounts: volumeMounts,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse("500m"),
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *HpcJobReconciler) constructMPIJob(hpcJob *ailsv1alpha1.HpcJob, queueName string) *mpiv2beta1.MPIJob {
	slots := hpcJob.Spec.Slots
	if slots <= 0 {
		slots = 2
	}

	var replicas int32 = 1
	if slots > 1 {
		replicas = slots / 2
		if slots%2 != 0 {
			replicas++
		}
	}

	labels := map[string]string{
		"kueue.x-k8s.io/queue-name":    queueName,
		"app.kubernetes.io/created-by": "hpc-controller",
	}

	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	if hpcJob.Spec.StorageSize != "" {
		pvcName := fmt.Sprintf("%s-pvc", hpcJob.Name)
		volumes = append(volumes, corev1.Volume{
			Name: "shared-workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "shared-workspace",
			MountPath: "/workspace",
		})
	}

	mpiJob := &mpiv2beta1.MPIJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hpcJob.Name,
			Namespace: hpcJob.Namespace,
			Labels:    labels,
		},
		Spec: mpiv2beta1.MPIJobSpec{
			SlotsPerWorker: &slots,
			MPIReplicaSpecs: map[mpiv2beta1.MPIReplicaType]*commonv1.ReplicaSpec{
				mpiv2beta1.MPIReplicaTypeLauncher: {
					Replicas: func() *int32 { i := int32(1); return &i }(),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Volumes: volumes,
							Containers: []corev1.Container{
								{
									Name:         "mpi-launcher",
									Image:        hpcJob.Spec.Image,
									Command:      hpcJob.Spec.Command,
									VolumeMounts: volumeMounts,
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("100m"),
										},
									},
								},
							},
						},
					},
				},
				mpiv2beta1.MPIReplicaTypeWorker: {
					Replicas: &replicas,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Volumes: volumes,
							Containers: []corev1.Container{
								{
									Name:         "mpi-worker",
									Image:        hpcJob.Spec.Image,
									VolumeMounts: volumeMounts,
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU: resource.MustParse("500m"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return mpiJob
}

func (r *HpcJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ailsv1alpha1.HpcJob{}).
		Owns(&mpiv2beta1.MPIJob{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}
