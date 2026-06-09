// Client-side AES-256-GCM for {{secure:...}} blocks.
//
// Two wire formats are supported:
//   {{secure_aes:base64(nonce(12) || ct+tag)}}   — legacy, key = SHA-256(passphrase)
//   {{secure_aes2:base64(nonce(12) || ct+tag)}}  — key = PBKDF2-HMAC-SHA256(passphrase, salt)
//
// Legacy blocks (and pages encrypted under the old GYPSUM_SECRET_KEY) stay
// readable with no migration. New blocks are written as secure_aes2 using the
// per-deployment salt served in window.gypsumSecureConfig. A single passphrase
// derives both keys, so a page may freely mix the two formats.
(function () {
  "use strict";

  var STORAGE_KEY = "gypsum-secret-key";
  var SUBTLE = window.crypto && window.crypto.subtle;

  var cfg = window.gypsumSecureConfig || { salt: "", iterations: 600000 };
  var ITERATIONS = cfg.iterations || 600000;

  // legacyKey decrypts {{secure_aes:...}}; pbkdf2Key decrypts/encrypts
  // {{secure_aes2:...}}. Raw bytes are kept for localStorage rehydration.
  var legacyKey = null, legacyBytes = null;
  var pbkdf2Key = null, pbkdf2Bytes = null;

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

  // saltBytes is null when no salt is configured; in that degraded mode new
  // blocks fall back to the legacy secure_aes format.
  var saltBytes = null;
  if (cfg.salt) {
    try { saltBytes = base64ToBytes(cfg.salt); } catch (_) { saltBytes = null; }
  }

  function notify() {
    for (var i = 0; i < listeners.length; i++) {
      try { listeners[i](isUnlocked()); } catch (_) { /* ignore */ }
    }
  }

  // Unlocked means we hold the key used to write new blocks: pbkdf2Key when a
  // salt is configured, otherwise the legacy key.
  function isUnlocked() {
    return saltBytes ? pbkdf2Key !== null : legacyKey !== null;
  }

  // pbkdf2Available reports whether new blocks should be written as secure_aes2.
  function pbkdf2Available() {
    return !!(saltBytes && pbkdf2Key);
  }
  function activeMacro() {
    return pbkdf2Available() ? "secure_aes2" : "secure_aes";
  }
  function activeKey() {
    return pbkdf2Available() ? pbkdf2Key : legacyKey;
  }

  async function importKeyBytes(rawBytes) {
    return SUBTLE.importKey("raw", rawBytes, { name: "AES-GCM" }, false, ["encrypt", "decrypt"]);
  }

  // SHA-256(passphrase) → 32-byte raw key (legacy KDF).
  async function sha256Bytes(passphrase) {
    var digest = await SUBTLE.digest("SHA-256", utf8(passphrase));
    return new Uint8Array(digest);
  }

  // PBKDF2-HMAC-SHA256(passphrase, salt, iterations) → 32-byte raw key.
  async function pbkdf2DeriveBytes(passphrase) {
    var base = await SUBTLE.importKey("raw", utf8(passphrase), { name: "PBKDF2" }, false, ["deriveBits"]);
    var bits = await SUBTLE.deriveBits(
      { name: "PBKDF2", salt: saltBytes, iterations: ITERATIONS, hash: "SHA-256" },
      base, 256
    );
    return new Uint8Array(bits);
  }

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({
        v: 2,
        legacy: legacyBytes ? bytesToHex(legacyBytes) : null,
        pbkdf2: pbkdf2Bytes ? bytesToHex(pbkdf2Bytes) : null,
        salt: cfg.salt || "",
      }));
    } catch (_) { /* quota / disabled */ }
  }

  async function unlock(passphrase, remember) {
    legacyBytes = await sha256Bytes(passphrase);
    legacyKey = await importKeyBytes(legacyBytes);
    if (saltBytes) {
      pbkdf2Bytes = await pbkdf2DeriveBytes(passphrase);
      pbkdf2Key = await importKeyBytes(pbkdf2Bytes);
    }
    if (remember) {
      persist();
    } else {
      try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
    }
    notify();
  }

  function lock() {
    legacyKey = legacyBytes = null;
    pbkdf2Key = pbkdf2Bytes = null;
    try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
    notify();
  }

  async function importLegacy(bytes) {
    if (bytes && bytes.length === 32) {
      legacyBytes = bytes;
      legacyKey = await importKeyBytes(bytes);
    }
  }

  // Rehydrate from localStorage on first load so persistent users skip the prompt.
  async function tryRehydrate() {
    var raw;
    try { raw = localStorage.getItem(STORAGE_KEY); } catch (_) { raw = null; }
    if (!raw) return;

    // New JSON format: {v:2, legacy, pbkdf2, salt}.
    if (raw.charAt(0) === "{") {
      var obj;
      try { obj = JSON.parse(raw); } catch (_) { obj = null; }
      if (!obj || obj.v !== 2) {
        try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
        return;
      }
      // A salt change invalidates the cached PBKDF2 key — force a re-unlock.
      if ((obj.salt || "") !== (cfg.salt || "")) {
        try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
        return;
      }
      try {
        if (obj.legacy) await importLegacy(hexToBytes(obj.legacy));
        if (obj.pbkdf2) {
          var pb = hexToBytes(obj.pbkdf2);
          if (pb && pb.length === 32) { pbkdf2Bytes = pb; pbkdf2Key = await importKeyBytes(pb); }
        }
      } catch (_) {
        lock();
      }
      return;
    }

    // Legacy bare-hex format from before secure_aes2: a SHA-256 key only. The
    // user keeps read access to old blocks and re-unlocks once to gain pbkdf2.
    var bytes = hexToBytes(raw);
    if (!bytes || bytes.length !== 32) {
      try { localStorage.removeItem(STORAGE_KEY); } catch (_) { /* ignore */ }
      return;
    }
    try { await importLegacy(bytes); } catch (_) { legacyKey = legacyBytes = null; }
  }

  async function encrypt(plaintext) {
    var key = activeKey();
    if (!key) throw new Error("Locked: no key available");
    var nonce = window.crypto.getRandomValues(new Uint8Array(12));
    var ct = await SUBTLE.encrypt({ name: "AES-GCM", iv: nonce }, key, utf8(plaintext));
    var ctBytes = new Uint8Array(ct);
    var combined = new Uint8Array(nonce.length + ctBytes.length);
    combined.set(nonce, 0);
    combined.set(ctBytes, nonce.length);
    return bytesToBase64(combined);
  }

  // decrypt selects the key by variant: "2" → pbkdf2Key, anything else → legacy.
  async function decrypt(b64, variant) {
    var key = variant === "2" ? pbkdf2Key : legacyKey;
    if (!key) throw new Error("Locked: no key available");
    var raw;
    try { raw = base64ToBytes(b64); } catch (_) { throw new Error("invalid base64"); }
    if (raw.length < 12) throw new Error("ciphertext too short");
    var nonce = raw.subarray(0, 12);
    var ct = raw.subarray(12);
    var plain = await SUBTLE.decrypt({ name: "AES-GCM", iv: nonce }, key, ct);
    return decodeUtf8(plain);
  }

  // --- Macro substitution helpers (mirrors internal/wiki/secure.go) ---

  // {{secure_aes:CT}} or {{secure_aes2:CT}}. Group 1 is the variant ("" or "2"),
  // group 2 is the base64 ciphertext.
  var SECURE_AES_RE = /\{\{secure_aes(2?):([A-Za-z0-9+/=]+)\}\}/g;
  // (\\?){{secure:(.*?)}} with DOTALL via [\s\S].
  var SECURE_RE = /(\\?)\{\{secure:([\s\S]*?)\}\}/g;

  // Replace every {{secure_aes[2]:CT}} in markdown with {{secure:plaintext}}. If a
  // ciphertext fails to decrypt, the original macro is left in place — the user
  // sees only the blocks their key can read, and broken ones round-trip safely.
  async function decryptForEdit(markdown) {
    var matches = [];
    var m;
    SECURE_AES_RE.lastIndex = 0;
    while ((m = SECURE_AES_RE.exec(markdown)) !== null) {
      matches.push({ start: m.index, end: m.index + m[0].length, variant: m[1], ct: m[2] });
    }
    if (matches.length === 0) return markdown;

    var plains = await Promise.all(matches.map(function (mm) {
      return decrypt(mm.ct, mm.variant).then(
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

  // Replace every {{secure:plain}} in markdown with {{secure_aes2:CT}} (or
  // {{secure_aes:CT}} when no salt is configured).
  // \{{secure:...}} stays literal (the editor keeps the leading backslash).
  // If oldMarkdown is provided, plaintexts that match an existing
  // {{secure_aes2:...}} block reuse the original ciphertext so unchanged blocks
  // produce no diff. Legacy {{secure_aes:...}} blocks are NOT reused, so editing
  // a page upgrades all of its secure blocks to the stronger KDF.
  async function encryptForSave(markdown, oldMarkdown) {
    var preserve = null;
    if (oldMarkdown) {
      preserve = Object.create(null);
      var oldMatches = [];
      var om;
      SECURE_AES_RE.lastIndex = 0;
      while ((om = SECURE_AES_RE.exec(oldMarkdown)) !== null) {
        if (om[1] === "2") oldMatches.push({ macro: om[0], ct: om[2] });
      }
      var plains = await Promise.all(oldMatches.map(function (mm) {
        return decrypt(mm.ct, "2").then(
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
        replacements.push("{{" + activeMacro() + ":" + ct + "}}");
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
