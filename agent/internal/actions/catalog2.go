package actions

// catalog2.go — Katalog-Batch 2 (Profi-Abdeckung): Secrets, generische Patches,
// PDB, PVC-Expand, Job-Delete und restore_resource (Snapshot-Wiederherstellung).
// Muster wie überall: trigger → verify, prepareUndo (Auto-Rollback bei Fehler/
// Cancel) + prepareRevert (inverse Katalog-Action für den Revert-Button).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

/* ── patch_secret ───────────────────────────────────────────────────────── */

type patchSecretParams struct {
	Key    string `json:"key"`
	Value  string `json:"value"` // plain ODER base64 — wird erkannt/normalisiert
	Remove bool   `json:"remove"`
}

// secretValueBytes akzeptiert Klartext und base64 (Konvention: wer base64
// schickt, will genau diese Bytes — decodierbar heißt decodieren).
func secretValueBytes(v string) []byte {
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && base64.StdEncoding.EncodeToString(b) == strings.TrimSpace(v) {
		return b
	}
	return []byte(v)
}

func (r *Runner) planPatchSecret(a Action) []step {
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p patchSecretParams
			if err := json.Unmarshal(a.Params, &p); err != nil || p.Key == "" {
				return "", fmt.Errorf("patch_secret requires params.key")
			}
			sec, err := r.clientset.CoreV1().Secrets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			if sec.Data == nil {
				sec.Data = map[string][]byte{}
			}
			if p.Remove {
				if _, ok := sec.Data[p.Key]; !ok {
					return "key already absent", nil
				}
				delete(sec.Data, p.Key)
			} else {
				sec.Data[p.Key] = secretValueBytes(p.Value)
			}
			if _, err := r.clientset.CoreV1().Secrets(a.TargetNamespace).Update(ctx, sec, metav1.UpdateOptions{FieldManager: "rocketplane-agent"}); err != nil {
				return "", err
			}
			if p.Remove {
				return fmt.Sprintf("removed key %q", p.Key), nil
			}
			return fmt.Sprintf("set key %q (%d bytes)", p.Key, len(sec.Data[p.Key])), nil
		}},
	}
}

/* ── create_configmap / delete_configmap ────────────────────────────────── */

func (r *Runner) planCreateConfigmap(a Action) []step {
	return []step{
		{name: "create", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(a.Params, &p); err != nil || len(p.Data) == 0 {
				return "", fmt.Errorf("create_configmap requires params.data")
			}
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: a.TargetName, Namespace: a.TargetNamespace},
				Data:       p.Data,
			}
			if _, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Create(ctx, cm, metav1.CreateOptions{FieldManager: "rocketplane-agent"}); err != nil {
				return "", err
			}
			return fmt.Sprintf("created with %d keys", len(p.Data)), nil
		}},
	}
}

func (r *Runner) planDeleteConfigmap(a Action) []step {
	return []step{
		{name: "delete", run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Delete(ctx, a.TargetName, metav1.DeleteOptions{}); err != nil {
				return "", err
			}
			return "deleted (before-state kept in the action snapshot)", nil
		}},
	}
}

/* ── pdb_set ────────────────────────────────────────────────────────────── */

type pdbSetParams struct {
	MinAvailable   *string `json:"minAvailable"`
	MaxUnavailable *string `json:"maxUnavailable"`
}

func intstrFrom(s string) intstr.IntOrString {
	return intstr.Parse(strings.TrimSpace(s))
}

func (r *Runner) planPDBSet(a Action) []step {
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p pdbSetParams
			if err := json.Unmarshal(a.Params, &p); err != nil || (p.MinAvailable == nil) == (p.MaxUnavailable == nil) {
				return "", fmt.Errorf("pdb_set requires exactly one of minAvailable/maxUnavailable")
			}
			pdb, err := r.clientset.PolicyV1().PodDisruptionBudgets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			if p.MinAvailable != nil {
				v := intstrFrom(*p.MinAvailable)
				pdb.Spec.MinAvailable = &v
				pdb.Spec.MaxUnavailable = nil
			} else {
				v := intstrFrom(*p.MaxUnavailable)
				pdb.Spec.MaxUnavailable = &v
				pdb.Spec.MinAvailable = nil
			}
			if _, err := r.clientset.PolicyV1().PodDisruptionBudgets(a.TargetNamespace).Update(ctx, pdb, metav1.UpdateOptions{FieldManager: "rocketplane-agent"}); err != nil {
				return "", err
			}
			return "pdb updated", nil
		}},
	}
}

/* ── patch_resource (generischer Merge-Patch, Whitelist) ────────────────── */

func (r *Runner) planPatchResource(a Action) []step {
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			if r.dyn == nil {
				return "", fmt.Errorf("generic access unavailable")
			}
			var p struct {
				Patch      map[string]any `json:"patch"`
				APIVersion string         `json:"apiVersion"`
				Resource   string         `json:"resource"`
			}
			if err := json.Unmarshal(a.Params, &p); err != nil || len(p.Patch) == 0 {
				return "", fmt.Errorf("patch_resource requires params.patch")
			}
			// GVR: statische Whitelist ODER CRD-Override (apiVersion+resource).
			// Der Override erlaubt Flux/Argo/cert-manager/CNPG-Patches; die
			// Gruppen-Denylist verhindert Privileg-Eskalation (RBAC/Webhooks/CRDs).
			var gvr = kindGVR[a.TargetKind]
			if p.APIVersion != "" && p.Resource != "" {
				g, err := rawGVR(p.APIVersion, a.TargetKind, p.Resource)
				if err != nil {
					return "", err
				}
				if deniedPatchGroup(g.Group) {
					return "", fmt.Errorf("patching group %q is not allowed (RBAC/webhooks/CRDs)", g.Group)
				}
				gvr = g
			}
			if (gvr == (schema.GroupVersionResource{})) {
				return "", fmt.Errorf("kind %q not patchable — pass params.apiVersion + params.resource for CRDs", a.TargetKind)
			}
			patch, _ := json.Marshal(p.Patch)
			if _, err := r.dyn.Resource(gvr).Namespace(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
				return "", err
			}
			return "merge patch applied", nil
		}},
	}
}

/* ── pvc_expand ─────────────────────────────────────────────────────────── */

func (r *Runner) planPVCExpand(a Action) []step {
	return []step{
		{name: "guard", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				Size string `json:"size"`
			}
			if err := json.Unmarshal(a.Params, &p); err != nil || p.Size == "" {
				return "", fmt.Errorf("pvc_expand requires params.size")
			}
			want, err := resource.ParseQuantity(p.Size)
			if err != nil {
				return "", fmt.Errorf("invalid size %q: %v", p.Size, err)
			}
			pvc, err := r.clientset.CoreV1().PersistentVolumeClaims(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			cur := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
			// HARTER Guard: PVCs können nur wachsen — alles andere lehnt der Agent ab.
			if want.Cmp(cur) <= 0 {
				return "", fmt.Errorf("pvc_expand only grows: current %s, requested %s", cur.String(), want.String())
			}
			return fmt.Sprintf("%s → %s", cur.String(), want.String()), nil
		}},
		{name: "expand", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				Size string `json:"size"`
			}
			_ = json.Unmarshal(a.Params, &p)
			patch := fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":%q}}}}`, p.Size)
			if _, err := r.clientset.CoreV1().PersistentVolumeClaims(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, []byte(patch), metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
				return "", err
			}
			return "resize requested (storage class must support expansion; may need pod restart)", nil
		}},
	}
}

/* ── delete_job ─────────────────────────────────────────────────────────── */

func (r *Runner) planDeleteJob(a Action) []step {
	return []step{
		{name: "delete", run: func(ctx context.Context, _ func(string)) (string, error) {
			prop := metav1.DeletePropagationBackground
			err := r.clientset.BatchV1().Jobs(a.TargetNamespace).Delete(ctx, a.TargetName, metav1.DeleteOptions{PropagationPolicy: &prop})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return "job already gone", nil
				}
				return "", err
			}
			return "job deleted (pods reaped in background)", nil
		}},
	}
}

/* ── restore_resource (Snapshot re-applyen) ─────────────────────────────── */

func (r *Runner) planRestoreResource(a Action) []step {
	return []step{
		{name: "restore", run: func(ctx context.Context, _ func(string)) (string, error) {
			if r.dyn == nil {
				return "", fmt.Errorf("generic access unavailable")
			}
			var p struct {
				Snapshot map[string]any `json:"snapshot"`
			}
			if err := json.Unmarshal(a.Params, &p); err != nil || len(p.Snapshot) == 0 {
				return "", fmt.Errorf("restore_resource requires params.snapshot")
			}
			u := &unstructured.Unstructured{Object: p.Snapshot}
			// Identität muss zum deklarierten Target passen — kein Objekt-Smuggling.
			if u.GetKind() != a.TargetKind || u.GetName() != a.TargetName {
				return "", fmt.Errorf("snapshot identity (%s/%s) does not match target (%s/%s)", u.GetKind(), u.GetName(), a.TargetKind, a.TargetName)
			}
			if ns := u.GetNamespace(); ns != "" && ns != a.TargetNamespace {
				return "", fmt.Errorf("snapshot namespace %q does not match target %q", ns, a.TargetNamespace)
			}
			gvr, ok := kindGVR[a.TargetKind]
			if !ok {
				return "", fmt.Errorf("kind %q not restorable", a.TargetKind)
			}
			data, _ := json.Marshal(p.Snapshot)
			// Server-Side-Apply mit force: der Snapshot ist die gewünschte Wahrheit.
			_, err := r.dyn.Resource(gvr).Namespace(a.TargetNamespace).Patch(ctx, a.TargetName, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "rocketplane-agent", Force: boolPtr(true)})
			if err != nil {
				return "", err
			}
			return "snapshot re-applied (server-side apply)", nil
		}},
	}
}

func boolPtr(b bool) *bool { return &b }

// deniedPatchGroup sperrt Gruppen, deren Patch Privilegien eskalieren würde —
// dieselbe Linie wie die Starlark-raw_*-Denylist.
func deniedPatchGroup(group string) bool {
	switch group {
	case "rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiextensions.k8s.io",
		"apiregistration.k8s.io",
		"certificates.k8s.io",
		"scheduling.k8s.io":
		return true
	}
	return false
}
