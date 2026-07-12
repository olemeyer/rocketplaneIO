package actions

// snapshot_crypto.go — Secret snapshots must be restorable (so patch_secret /
// delete of a Secret can be reverted) WITHOUT ever persisting plaintext to the
// control plane. The agent encrypts a Secret capture's payload with AES-256-GCM
// under a key derived from its long-lived agent token — a per-cluster secret that
// never leaves the cluster and is shared by agent replicas. The control plane only
// ever stores opaque ciphertext; Restore (this agent or a peer) decrypts it.
//
// Trade-off: rotating the agent token makes older Secret ciphertext undecryptable
// (a very old Secret revert after re-enrollment); that is an accepted edge.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
)

func (r *Runner) snapCipher() (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte("rocketplane-snapshot-v1|" + r.token))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type secretPayload struct {
	Object map[string]any `json:"object,omitempty"`
	Fields []FieldDelta   `json:"fields,omitempty"`
}

// encryptForDurable returns a copy of the capture safe to persist off-cluster. For
// a Secret it encrypts {object,fields} into Enc and clears the plaintext; if
// encryption is somehow impossible it fails CLOSED (drops the payload — no revert,
// but never plaintext). Non-secret captures pass through unchanged.
func (r *Runner) encryptForDurable(c Capture) Capture {
	if c.Kind != "Secret" || (c.Object == nil && len(c.Fields) == 0) {
		return c
	}
	out := c
	out.Object, out.Fields = nil, nil // never leave plaintext on the wire
	aead, err := r.snapCipher()
	if err != nil {
		return out
	}
	pt, err := json.Marshal(secretPayload{Object: c.Object, Fields: c.Fields})
	if err != nil {
		return out
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return out
	}
	out.Enc = base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, pt, nil))
	return out
}

// decryptCapture reverses encryptForDurable: an Enc-carrying capture is decrypted
// back to Object/Fields so the generic Restore can replay it. Best-effort — an
// undecryptable capture (rotated key, tamper) is returned as-is (Restore then
// no-ops the empty payload) rather than failing the whole revert.
func (r *Runner) decryptCapture(c Capture) Capture {
	if c.Enc == "" {
		return c
	}
	out := c
	out.Enc = ""
	aead, err := r.snapCipher()
	if err != nil {
		return out
	}
	raw, err := base64.StdEncoding.DecodeString(c.Enc)
	if err != nil || len(raw) < aead.NonceSize() {
		return out
	}
	pt, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return out
	}
	var p secretPayload
	if err := json.Unmarshal(pt, &p); err != nil {
		return out
	}
	out.Object, out.Fields = p.Object, p.Fields
	return out
}
