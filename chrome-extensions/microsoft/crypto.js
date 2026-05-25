// AES-256-GCM encryption matching the Go implementation in internal/crypto/crypto.go
// Format: salt (32 bytes) || nonce (12 bytes) || ciphertext

const SALT_LEN = 32;
const NONCE_LEN = 12;

/**
 * Derive a 256-bit key from a passphrase and salt using PBKDF2.
 */
async function deriveKey(passphrase, salt, usage) {
  const encoder = new TextEncoder();
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    encoder.encode(passphrase),
    "PBKDF2",
    false,
    ["deriveKey"]
  );
  return crypto.subtle.deriveKey(
    {
      name: "PBKDF2",
      salt: salt,
      iterations: 100000,
      hash: "SHA-256",
    },
    keyMaterial,
    { name: "AES-GCM", length: 256 },
    false,
    usage
  );
}

/**
 * Encrypt plaintext with passphrase using AES-256-GCM.
 * Returns base64-encoded: salt || nonce || ciphertext
 */
async function encrypt(plaintext, passphrase) {
  const encoder = new TextEncoder();
  const salt = crypto.getRandomValues(new Uint8Array(SALT_LEN));
  const nonce = crypto.getRandomValues(new Uint8Array(NONCE_LEN));

  const key = await deriveKey(passphrase, salt, ["encrypt"]);
  const ciphertext = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv: nonce },
    key,
    encoder.encode(plaintext)
  );

  const result = new Uint8Array(SALT_LEN + NONCE_LEN + ciphertext.byteLength);
  result.set(salt, 0);
  result.set(nonce, SALT_LEN);
  result.set(new Uint8Array(ciphertext), SALT_LEN + NONCE_LEN);

  return btoa(String.fromCharCode(...result));
}

/**
 * Decrypt base64-encoded ciphertext with passphrase.
 * Input format: base64(salt || nonce || ciphertext)
 */
async function decrypt(encoded, passphrase) {
  const raw = Uint8Array.from(atob(encoded), (c) => c.charCodeAt(0));

  if (raw.length < SALT_LEN + NONCE_LEN + 1) {
    throw new Error("ciphertext too short");
  }

  const salt = raw.slice(0, SALT_LEN);
  const nonce = raw.slice(SALT_LEN, SALT_LEN + NONCE_LEN);
  const ciphertext = raw.slice(SALT_LEN + NONCE_LEN);

  const key = await deriveKey(passphrase, salt, ["decrypt"]);
  const plaintext = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: nonce },
    key,
    ciphertext
  );

  return new TextDecoder().decode(plaintext);
}

// Export for use in other scripts
if (typeof globalThis !== "undefined") {
  globalThis.teamsCLICrypto = { encrypt, decrypt };
}
