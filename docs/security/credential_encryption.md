# Credential Encryption

Rhizome supports encrypting `api_key`/`api_keys` values in `model_list` configuration entries.
Encrypted keys are stored as `enc2://<base64>` strings and decrypted automatically at startup.
Legacy `enc://` values (AES-256-GCM) still decrypt transparently.

---

## Quick Start

**1. Set your passphrase**

```bash
export RHIZOME_KEY_PASSPHRASE="your-passphrase"
```

**2. Encrypt an API key**

Run `rhizome onboard` — it prompts for your passphrase and generates the SSH key,
then automatically re-encrypts any plaintext `api_key` entries in your config on
the next `SaveConfig` call. The resulting `enc2://` value will look like:

```
enc2://AAAA...base64...
```

**3. Paste the output into your config**

```json
{
  "model_list": [
    {
      "model_name": "gpt-4o",
      "model": "openai/gpt-4o",
      // "api_keys": ["enc2://AAAA...base64..."] move to .security.yml
      "api_base": "https://api.openai.com/v1"
    }
  ]
}
```

---

## Supported `api_key` Formats

The same formats apply to both `api_key` (singular) and individual elements in the `api_keys` (array) field:

| Format | Example | Behaviour |
|--------|---------|-----------|
| Plaintext | `sk-abc123` | Used as-is |
| File reference | `file://openai.key` | Content read from the same directory as the config file |
| Encrypted | `enc2://<base64>` | XChaCha20-Poly1305, decrypted at startup (current write format) |
| Encrypted (legacy) | `enc://<base64>` | AES-256-GCM, decrypted at startup; re-encrypted to `enc2://` on next save |
| Empty | `""` | Passed through unchanged (used with `auth_method: oauth`) |

---

## Cryptographic Design

### Key Derivation

Encryption uses **HKDF-SHA256** with an SSH private key as a second factor.
The HKDF `info` field is versioned per scheme (`rhizome-credential-v1` for
`enc://`, `rhizome-credential-v2` for `enc2://`), so keys derived for one
scheme can never decrypt the other.

```
sshHash = SHA256(ssh_private_key_file_bytes)
ikm     = HMAC-SHA256(key=sshHash, message=passphrase)
aeadKey = HKDF-SHA256(ikm, salt, info="rhizome-credential-v2", 32 bytes)
```

### Encryption

```
XChaCha20-Poly1305(key=aeadKey, nonce=random[24], plaintext=api_key)
```

### Wire Format

```
enc2://<base64( salt[16] + nonce[24] + ciphertext )>
```

| Field | Size | Description |
|-------|------|-------------|
| `salt` | 16 bytes | Random per encryption; fed into HKDF |
| `nonce` | 24 bytes | Random per encryption; XChaCha20 extended nonce |
| `ciphertext` | variable | XChaCha20-Poly1305 ciphertext + 16-byte authentication tag |

The Poly1305 authentication tag is appended to the ciphertext automatically.
Any tampering causes decryption to fail with an error rather than returning
corrupt plaintext. XChaCha20's 192-bit nonce makes random-nonce reuse
cryptographically negligible, removing the birthday-bound concern that
applies to 96-bit GCM nonces at scale.

### Legacy `enc://` (v1)

Pre-v0.12.0 blobs are `enc://<base64( salt[16] + nonce[12] + AES-256-GCM ct )>`
with `info="rhizome-credential-v1"`. Decryption is transparent — the resolver
dispatches on the scheme prefix. New writes are always `enc2://`; this is a
**one-way door**: once a value is re-saved it is `enc2://` and older Rhizome
versions (< v0.12.0) cannot read it. To roll a whole config forward, set the
passphrase and re-save (`SaveConfig` re-encrypts any `enc://` values it
encounters via `SecureString.MarshalYAML`).

### Performance

| Operation | Time (ARM Cortex-A) |
|-----------|---------------------|
| Key derivation (HKDF) | < 1 ms |
| AES-256-GCM decrypt | < 1 ms |
| **Total startup overhead** | **< 2 ms per key** |

---

## Two-Factor Security with SSH Key

When a SSH private key is provided, breaking the encryption requires **both**:

1. The **passphrase** (`RHIZOME_KEY_PASSPHRASE`)
2. The **SSH private key file**

This means a leaked config file alone is not sufficient to recover the API key, even if the passphrase is weak. The SSH key contributes 256 bits of entropy (Ed25519) regardless of passphrase strength.

### Threat Model

| Attacker Has | Can Decrypt? |
|---|---|
| Config file only | No — needs passphrase + SSH key |
| SSH key only | No — needs passphrase |
| Passphrase only | No — needs SSH key |
| Config file + SSH key + passphrase | Yes — full compromise |

---

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `RHIZOME_KEY_PASSPHRASE` | Yes (for `enc://`/`enc2://`) | Passphrase used for key derivation |
| `RHIZOME_SSH_KEY_PATH` | No | Path to SSH private key. If not set, auto-detects from `~/.ssh/rhizome_ed25519.key` |

### SSH Key Auto-Detection

If `RHIZOME_SSH_KEY_PATH` is not set, Rhizome looks for the rhizome-specific key:

```
~/.ssh/rhizome_ed25519.key
```

This dedicated file avoids conflicts with the user's existing SSH keys.
Run `rhizome onboard` to generate it automatically.

`os.UserHomeDir()` is used for cross-platform home directory resolution (reads `USERPROFILE` on Windows, `HOME` on Unix/macOS).

> **Note:** An SSH key file is required for credential encryption. If no key is found and `RHIZOME_SSH_KEY_PATH` is not set, encryption/decryption will fail. Run `rhizome onboard` to generate the key automatically.

---

## Migration

Because the only secret material is `RHIZOME_KEY_PASSPHRASE` and the SSH private key file, migration is straightforward:

1. Copy the config file to the new machine.
2. Set `RHIZOME_KEY_PASSPHRASE` to the same value.
3. Copy the SSH private key file to the same path (or set `RHIZOME_SSH_KEY_PATH` to its new location).

No re-encryption is needed.

---

## Security Considerations

- **Both passphrase and SSH key are required.** The SSH key acts as a second factor — without it, encryption/decryption will fail. Run `rhizome onboard` to generate the key if it doesn't exist.
- **The SSH key is read-only at runtime.** Rhizome never writes to or modifies the SSH key file.
- **Plaintext keys remain supported.** Existing configs without `enc://` are unaffected.
- **The format is versioned** via the HKDF `info` field (`rhizome-credential-v1`/`-v2`) and the `enc://`/`enc2://` scheme prefix — algorithm upgrades never break existing encrypted values, but downgrading rhizome below v0.12.0 loses access to `enc2://` entries.
