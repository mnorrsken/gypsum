// Client-side AES-256-GCM for {{secure:...}} blocks.
//
// Wire format matches the server's historical layout:
//   {{secure_aes:base64(nonce(12) || ciphertext+tag)}}
// Key derivation matches the historical server KDF: SHA-256(passphrase).
// That makes pages encrypted under the old GYPSUM_SECRET_KEY decrypt with the
// same passphrase entered here in the browser, with no migration.
(function () {
  "use strict";

  var STORAGE_KEY = "gypsum-secret-key"; // hex-encoded 32-byte raw key
  var SUBTLE = window.crypto && window.crypto.subtle;

  var memKey = null;          // imported AES-GCM CryptoKey, or null when locked
  var memKeyBytes = null;     // raw 32-byte Uint8Array, kept for storage rehydration

  var listeners = [];         // notified on lock/unlock so UI can refresh
  var pendingUnlock = null;   // resolves when the user finishes the unlock modal

  function utf8(s) {
    return new TextEncoder().encode(s);
  }
  function decodeUtf8(buf) {
    return new TextDecoder().decode(buf);
  }
  function bytesToBase64(bytes) {
    var s = "";
    for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s);
  }
  function base64ToBytes(b64) {
    var s = atob(b64);
    var out = new Uint8Array(s.length);
    for (var i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out;
  }
  function bytesToHex(bytes) {
    var s = "";
    for (var i = 0; i < bytes.length; i++) {
      var h = bytes[i].toString(16);
      if (h.length === 1) h = "0" + h;
      s += h;
    }
    return s;
  }
  function hexToBytes(hex) {
    if (hex.length % 2 !== 0) return null;
    var out = new Uint8Array(hex.length / 2);
    for (var i = 0; i < out.length; i++) {
      var b = parseInt(hex.substr(i * 2, 2), 16);
      if (isNaN(b)) return null;
      out[i] = b;
    }
    return out;
  }

  function notify() {
    for (var i = 0; i < listeners.length; i++) {
      try { listeners[i](isUnlocked()); } catch (_) { /* ignore */ }
    }
  }

  function isUnlocked() {
    return memKey !== null;
  }

  async function importKeyBytes(rawBytes) {
    return SUBTLE.importKey("raw", rawBytes, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
  }

  // SHA-256(passphrase) → 32-byte raw key. Matches the old server-side KDF in
  // internal/wiki/secure.go.
  async function deriveBytes(passphrase) {
    var digest = await SUBTLE.digest("SHA-256", utf8(passphrase));
    return new Uint8Array(digest);
  }

  async function unlock(passphrase, remember) {
    var bytes = await deriveBytes(passphrase);
    var key = await importKeyBytes(bytes);
    memKey = key;
    memKeyBytes = bytes;
    if (remember) {
      try { localStorage.setItem(STORAGE_KEY, bytesToHex(bytes)); } catch (_) { /* quota / disabled */ }
    } else {
      try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
    }
    notify();
  }

  function lock() {
    memKey = null;
    memKeyBytes = null;
    try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
    notify();
  }

  // Rehydrate from localStorage on first load so persistent users skip the prompt.
  async function tryRehydrate() {
    var hex;
    try { hex = localStorage.getItem(STORAGE_KEY); } catch (_) { hex = null; }
    if (!hex) return;
    var bytes = hexToBytes(hex);
    if (!bytes || bytes.length !== 32) {
      try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
      return;
    }
    try {
      memKey = await importKeyBytes(bytes);
      memKeyBytes = bytes;
    } catch (_) {
      memKey = null;
      memKeyBytes = null;
    }
  }

  async function encrypt(plaintext) {
    if (!memKey) throw new Error("Locked: no key available");
    var nonce = window.crypto.getRandomValues(new Uint8Array(12));
    var ct = await SUBTLE.encrypt({ name: "AES-GCM", iv: nonce }, memKey, utf8(plaintext));
    var ctBytes = new Uint8Array(ct);
    var combined = new Uint8Array(nonce.length + ctBytes.length);
    combined.set(nonce, 0);
    combined.set(ctBytes, nonce.length);
    return bytesToBase64(combined);
  }

  async function decrypt(b64) {
    if (!memKey) throw new Error("Locked: no key available");
    var raw;
    try { raw = base64ToBytes(b64); } catch (_) { throw new Error("invalid base64"); }
    if (raw.length < 12) throw new Error("ciphertext too short");
    var nonce = raw.subarray(0, 12);
    var ct = raw.subarray(12);
    var plain = await SUBTLE.decrypt({ name: "AES-GCM", iv: nonce }, memKey, ct);
    return decodeUtf8(plain);
  }

  // --- Macro substitution helpers (mirrors internal/wiki/secure.go) ---

  // {{secure_aes:BASE64}} where BASE64 is [A-Za-z0-9+/=]+.
  var SECURE_AES_RE = /\{\{secure_aes:([A-Za-z0-9+/=]+)\}\}/g;
  // (\\?){{secure:(.*?)}} with DOTALL via [\s\S].
  var SECURE_RE = /(\\?)\{\{secure:([\s\S]*?)\}\}/g;

  // Replace every {{secure_aes:CT}} in markdown with {{secure:plaintext}}. If a
  // ciphertext fails to decrypt, the original macro is left in place — the user
  // sees only the blocks their key can read, and broken ones round-trip safely.
  async function decryptForEdit(markdown) {
    var matches = [];
    var m;
    SECURE_AES_RE.lastIndex = 0;
    while ((m = SECURE_AES_RE.exec(markdown)) !== null) {
      matches.push({ start: m.index, end: m.index + m[0].length, ct: m[1] });
    }
    if (matches.length === 0) return markdown;

    var plains = await Promise.all(matches.map(function (mm) {
      return decrypt(mm.ct).then(
        function (p) { return { ok: true, plain: p }; },
        function ()  { return { ok: false }; }
      );
    }));

    var out = "";
    var pos = 0;
    for (var i = 0; i < matches.length; i++) {
      out += markdown.substring(pos, matches[i].start);
      if (plains[i].ok) {
        var p = plains[i].plain;
        if (p.indexOf("\n") >= 0) {
          out += "{{secure:\n" + p + "\n}}";
        } else {
          out += "{{secure:" + p + "}}";
        }
      } else {
        out += markdown.substring(matches[i].start, matches[i].end);
      }
      pos = matches[i].end;
    }
    out += markdown.substring(pos);
    return out;
  }

  // Replace every {{secure:plain}} in markdown with {{secure_aes:CT}}.
  // \{{secure:...}} stays literal (the editor keeps the leading backslash).
  // If oldMarkdown is provided, plaintexts that match an existing
  // {{secure_aes:...}} block in oldMarkdown reuse the original ciphertext so
  // unchanged blocks produce no diff (mirrors EncryptForSavePreserving in Go).
  async function encryptForSave(markdown, oldMarkdown) {
    // Build plaintext → original-ciphertext-macro map from oldMarkdown.
    var preserve = null;
    if (oldMarkdown) {
      preserve = Object.create(null);
      var oldMatches = [];
      var om;
      SECURE_AES_RE.lastIndex = 0;
      while ((om = SECURE_AES_RE.exec(oldMarkdown)) !== null) {
        oldMatches.push({ macro: om[0], ct: om[1] });
      }
      var plains = await Promise.all(oldMatches.map(function (mm) {
        return decrypt(mm.ct).then(
          function (p) { return p; },
          function ()  { return null; }
        );
      }));
      for (var i = 0; i < oldMatches.length; i++) {
        if (plains[i] !== null) preserve[plains[i]] = oldMatches[i].macro;
      }
    }

    // Walk {{secure:...}} blocks in order.
    var matches = [];
    var m;
    SECURE_RE.lastIndex = 0;
    while ((m = SECURE_RE.exec(markdown)) !== null) {
      matches.push({
        start: m.index,
        end: m.index + m[0].length,
        escaped: m[1] === "\\",
        content: m[2],
        full: m[0],
      });
    }
    if (matches.length === 0) return markdown;

    var replacements = [];
    for (var j = 0; j < matches.length; j++) {
      var mm = matches[j];
      if (mm.escaped) {
        // Preserve \{{secure:...}} verbatim.
        replacements.push(mm.full);
        continue;
      }
      var content = mm.content;
      // Strip leading/trailing newlines for multiline blocks (matches Go).
      if (content.length > 0 && content.charAt(0) === "\n" &&
          content.charAt(content.length - 1) === "\n") {
        content = content.substring(1, content.length - 1);
      }
      if (preserve && Object.prototype.hasOwnProperty.call(preserve, content)) {
        replacements.push(preserve[content]);
      } else {
        var ct = await encrypt(content);
        replacements.push("{{secure_aes:" + ct + "}}");
      }
    }

    var out = "";
    var pos = 0;
    for (var k = 0; k < matches.length; k++) {
      out += markdown.substring(pos, matches[k].start);
      out += replacements[k];
      pos = matches[k].end;
    }
    out += markdown.substring(pos);
    return out;
  }

  // requireKey resolves once the user has unlocked. If already unlocked it
  // resolves immediately. Otherwise it opens the lock UI and waits.
  function requireKey() {
    if (isUnlocked()) return Promise.resolve();
    if (pendingUnlock) return pendingUnlock;
    pendingUnlock = new Promise(function (resolve, reject) {
      var off = onChange(function (unlocked) {
        if (unlocked) {
          off();
          pendingUnlock = null;
          resolve();
        }
      });
      // openLockUI is wired by the lock-status partial.
      if (typeof window.gypsumOpenLockUI === "function") {
        window.gypsumOpenLockUI(function () {
          // User cancelled without unlocking.
          if (!isUnlocked()) {
            off();
            pendingUnlock = null;
            reject(new Error("Unlock cancelled"));
          }
        });
      } else {
        // Fallback: a plain prompt() so we don't hard-fail without the partial.
        var pw = window.prompt("Enter encryption passphrase");
        if (pw === null) {
          off();
          pendingUnlock = null;
          reject(new Error("Unlock cancelled"));
          return;
        }
        unlock(pw, false).then(function () {
          // notify already fired
        }, function (err) {
          off();
          pendingUnlock = null;
          reject(err);
        });
      }
    });
    return pendingUnlock;
  }

  function onChange(fn) {
    listeners.push(fn);
    return function off() {
      var idx = listeners.indexOf(fn);
      if (idx >= 0) listeners.splice(idx, 1);
    };
  }

  // Boot: rehydrate the key from localStorage before any other script that
  // depends on it runs. We expose a ready promise so callers can await it.
  var ready = SUBTLE ? tryRehydrate() : Promise.resolve();

  window.gypsumSecure = {
    ready: ready,
    isUnlocked: isUnlocked,
    unlock: unlock,
    lock: lock,
    encrypt: encrypt,
    decrypt: decrypt,
    decryptForEdit: decryptForEdit,
    encryptForSave: encryptForSave,
    requireKey: requireKey,
    onChange: onChange,
  };
})();
