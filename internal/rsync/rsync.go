package rsync

import (
	"bytes"
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/utkuozdemir/pv-migrate/internal/k8s"
	"github.com/utkuozdemir/pv-migrate/internal/pvc"
	"github.com/utkuozdemir/pv-migrate/internal/task"
	"html/template"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	maxRetries            = 10
	retryIntervalSecs     = 5
	sshConnectTimeoutSecs = 5
	pspName               = "pv-migrate"
)

var scriptTemplate = template.Must(template.New("script").Parse(`
n=0
rc=1
retries={{.MaxRetries}}
until [ "$n" -ge "$retries" ]
do
  rsync \
    -avzh \
    --progress \
    {{ if .DeleteExtraneousFiles -}}
    --delete \
    {{ end -}}
    {{ if .NoChown -}}
    --no-o --no-g \
    {{ end -}}
    {{ if .SshTargetHost -}}
    -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout={{.SshConnectTimeoutSecs}}" \
    root@{{.SshTargetHost}}:/source/ \
    {{ else -}}
    /source/ \
    {{ end -}}
    /dest/ && \
    rc=0 && \
    break
  n=$((n+1))
  echo "rsync attempt $n/{{.MaxRetries}} failed, waiting {{.RetryIntervalSecs}} seconds before trying again"
  sleep {{.RetryIntervalSecs}}
done

if [ $rc -ne 0 ]; then
  echo "Rsync job failed after $retries retries"
fi
exit $rc
`))

type script struct {
	MaxRetries            int
	DeleteExtraneousFiles bool
	NoChown               bool
	SshTargetHost         string
	SshConnectTimeoutSecs int
	RetryIntervalSecs     int
}

func BuildRsyncScript(deleteExtraneousFiles bool, noChown bool, sshTargetHost string) (string, error) {
	s := script{
		MaxRetries:            maxRetries,
		DeleteExtraneousFiles: deleteExtraneousFiles,
		NoChown:               noChown,
		SshTargetHost:         sshTargetHost,
		SshConnectTimeoutSecs: sshConnectTimeoutSecs,
		RetryIntervalSecs:     retryIntervalSecs,
	}

	var templatedScript bytes.Buffer
	err := scriptTemplate.Execute(&templatedScript, s)
	if err != nil {
		return "", err
	}

	return templatedScript.String(), nil
}

func createRsyncPrivateKeySecret(instanceId string, pvcInfo *pvc.Info, privateKey string) (*corev1.Secret, error) {
	kubeClient := pvcInfo.KubeClient
	namespace := pvcInfo.Claim.Namespace
	name := "pv-migrate-rsync-" + instanceId
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    k8s.ComponentLabels(instanceId, k8s.Rsync),
		},
		Data: map[string][]byte{
			"privateKey": []byte(privateKey),
		},
	}

	secrets := kubeClient.CoreV1().Secrets(namespace)
	return secrets.Create(context.TODO(), &secret, metav1.CreateOptions{})
}

func buildRsyncJobDest(t *task.Task, targetHost string, privateKeySecretName string, svcAccName string) (*batchv1.Job, error) {
	jobTTLSeconds := int32(600)
	backoffLimit := int32(0)
	id := t.ID
	jobName := "pv-migrate-rsync-" + id
	d := t.DestInfo

	opts := t.Migration.Options
	rsyncScript, err := BuildRsyncScript(opts.DeleteExtraneousFiles,
		opts.NoChown, targetHost)
	if err != nil {
		return nil, err
	}

	permissions := int32(256) // octal mode 0400 - we don't need more than that
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: d.Claim.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &jobTTLSeconds,
			Template: corev1.PodTemplateSpec{

				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName,
					Namespace: d.Claim.Namespace,
					Labels:    k8s.ComponentLabels(id, k8s.Rsync),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: svcAccName,
					Volumes: []corev1.Volume{
						{
							Name: "dest-vol",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: d.Claim.Name,
								},
							},
						},
						{
							Name: "private-key-vol",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName:  privateKeySecretName,
									DefaultMode: &permissions,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: t.Migration.RsyncImage,
							Command: []string{
								"sh",
								"-c",
								rsyncScript,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dest-vol",
									MountPath: "/dest",
								},
								{
									Name:      "private-key-vol",
									MountPath: fmt.Sprintf("/root/.ssh/id_%s", t.Migration.Options.KeyAlgorithm),
									SubPath:   "privateKey",
								},
							},
						},
					},
					NodeName:      d.MountedNode,
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
	return &job, nil
}

func RunRsyncJobOverSSH(t *task.Task, serviceType corev1.ServiceType) error {
	instanceId := t.ID
	s := t.SourceInfo
	sourceKubeClient := s.KubeClient
	d := t.DestInfo
	destKubeClient := d.KubeClient

	sourceSvcAccName := "default"
	if t.Migration.Options.SourceCreatePSP {
		sa, err := createPSPResources(s.KubeClient, instanceId, s.Claim.Namespace)
		if err != nil {
			return err
		}
		sourceSvcAccName = sa
	}

	destSvcAccName := "default"
	if t.Migration.Options.DestCreatePSP {
		sa, err := createPSPResources(d.KubeClient, instanceId, d.Claim.Namespace)
		if err != nil {
			return err
		}
		destSvcAccName = sa
	}

	log.Info("Generating SSH key pair")
	publicKey, privateKey, err := CreateSSHKeyPair(t.Migration.Options.KeyAlgorithm)
	if err != nil {
		return err
	}

	log.Info("Creating secret for the public key")
	secret, err := createSshdPublicKeySecret(instanceId, s, publicKey)
	if err != nil {
		return err
	}

	sftpPod := PrepareSshdPod(instanceId, s, secret.Name, t.Migration.SshdImage, sourceSvcAccName)
	err = CreateSshdPodWaitTillRunning(sourceKubeClient, sftpPod)
	if err != nil {
		return err
	}

	createdService, err := CreateSshdService(instanceId, s, serviceType)
	if err != nil {
		return err
	}
	targetHost, err := k8s.GetServiceAddress(sourceKubeClient, createdService)
	if err != nil {
		return err
	}

	log.Info("Creating secret for the private key")
	secret, err = createRsyncPrivateKeySecret(instanceId, d, privateKey)
	if err != nil {
		return err
	}

	log.WithField("targetHost", targetHost).Info("Connecting to the rsync server")
	rsyncJob, err := buildRsyncJobDest(t, targetHost, secret.Name, destSvcAccName)
	if err != nil {
		return err
	}

	err = k8s.CreateJobWaitTillCompleted(destKubeClient, rsyncJob)
	if err != nil {
		return err
	}
	return nil
}

func createPSPResources(c kubernetes.Interface, id string, ns string) (string, error) {
	err := ensurePSP(c)
	if err != nil {
		return "", err
	}

	name := fmt.Sprintf("pv-migrate-%s", id)

	sa := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err = c.CoreV1().ServiceAccounts(ns).Create(context.TODO(), &sa, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	role := v1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pv-migrate-%s", id),
			Namespace: ns,
		},
		Rules: []v1.PolicyRule{
			{
				Verbs:         []string{"use"},
				APIGroups:     []string{"policy"},
				Resources:     []string{"podsecuritypolicies"},
				ResourceNames: []string{pspName},
			},
		},
	}

	_, err = c.RbacV1().Roles(ns).Create(context.TODO(), &role, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	rb := v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Subjects: []v1.Subject{
			{
				Kind: "ServiceAccount",
				Name: name,
			},
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}

	_, err = c.RbacV1().RoleBindings(ns).Create(context.TODO(), &rb, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	return name, nil
}

func ensurePSP(c kubernetes.Interface) error {
	psp := v1beta1.PodSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: pspName,
		},
		Spec: v1beta1.PodSecurityPolicySpec{
			RunAsUser: v1beta1.RunAsUserStrategyOptions{
				Rule: v1beta1.RunAsUserStrategyRunAsAny,
			},
			RunAsGroup: &v1beta1.RunAsGroupStrategyOptions{
				Rule: v1beta1.RunAsGroupStrategyRunAsAny,
			},
			FSGroup: v1beta1.FSGroupStrategyOptions{
				Rule: v1beta1.FSGroupStrategyRunAsAny,
			},
			SELinux: v1beta1.SELinuxStrategyOptions{
				Rule: v1beta1.SELinuxStrategyRunAsAny,
			},
			SupplementalGroups: v1beta1.SupplementalGroupsStrategyOptions{
				Rule: v1beta1.SupplementalGroupsStrategyRunAsAny,
			},
			Volumes: []v1beta1.FSType{v1beta1.Secret, v1beta1.PersistentVolumeClaim},
		},
	}

	_, err := c.PolicyV1beta1().PodSecurityPolicies().Create(context.TODO(), &psp, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return nil
	}

	return err
}
