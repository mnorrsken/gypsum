// Secrets vault: a searchable list of encrypted credentials.
//
// The secret value never leaves the browser in cleartext — it is encrypted with
// window.gypsumSecure (the same passphrase and {{secure_aes2}} wire format used
// by secure blocks on wiki pages) before it is POSTed, and decrypted here on
// reveal or copy. The server only ever holds ciphertext plus metadata.
(function () {
  "use strict";

  var list = document.getElementById("secrets-list");
  if (!list) return;

  var HOLD_MS = (parseInt(list.dataset.hold || "60", 10) || 60) * 1000;
  var HUES = parseInt(list.dataset.hues || "12", 10) || 12;
  var MASK = "••••••••••••";
  var COPY_ICON = "📋";
  var enc = new TextEncoder();
  var revealTimers = new WeakMap();

  var filterInput = document.getElementById("secret-filter");
  var emptyMsg = document.getElementById("secrets-empty");

  // ── Title-derived presentation (mirrors secrets.go) ──────────────────

  // secretColor mirrors SecretColor in secrets.go: FNV-1a over the UTF-8 bytes
  // of the trimmed, lowercased title. Keep the two in sync.
  function secretColor(title) {
    var bytes = enc.encode(title.trim().toLowerCase());
    var h = 0x811c9dc5;
    for (var i = 0; i < bytes.length; i++) {
      h ^= bytes[i];
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h % HUES;
  }

  // secretMnemonic mirrors SecretMnemonic in secrets.go: the initials of the
  // first two words, or the first two letters of a single word.
  function secretMnemonic(title) {
    var words = (title.match(/[\p{L}\p{N}]+/gu) || []);
    if (words.length === 0) return "?";
    if (words.length >= 2) {
      return (firstChars(words[0], 1) + firstChars(words[1], 1)).toUpperCase();
    }
    return firstChars(words[0], 2).toUpperCase();
  }

  function firstChars(s, n) {
    return Array.from(s).slice(0, n).join("");
  }

  // ── Filtering ────────────────────────────────────────────────────────

  function applyFilter() {
    var q = (filterInput ? filterInput.value : "").trim().toLowerCase();
    var terms = q ? q.split(/\s+/) : [];
    var cards = list.querySelectorAll(".secret-card");
    var visible = 0;
    cards.forEach(function (card) {
      var hay = (card.dataset.search || "").toLowerCase();
      var match = terms.every(function (t) { return hay.indexOf(t) >= 0; });
      card.hidden = !match;
      if (match) visible++;
    });
    if (emptyMsg) {
      emptyMsg.hidden = visible > 0;
      if (visible === 0) {
        emptyMsg.textContent = cards.length === 0
          ? "No secrets yet. Add one to get started."
          : "No secrets match that filter.";
      }
    }
  }

  if (filterInput) {
    filterInput.addEventListener("input", applyFilter);
    // Escape clears the filter rather than closing the page's dialog.
    filterInput.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && filterInput.value) {
        e.stopPropagation();
        filterInput.value = "";
        applyFilter();
      }
    });
  }

  // ── Reveal / copy ────────────────────────────────────────────────────

  function mask(card) {
    var pre = card.querySelector(".secret-value");
    if (!pre) return;
    clearTimeout(revealTimers.get(card));
    revealTimers.delete(card);
    pre.textContent = MASK;
    pre.classList.remove("revealed");
  }

  async function reveal(card) {
    var pre = card.querySelector(".secret-value");
    var ct = card.dataset.ciphertext;
    if (!pre || !ct) return;
    if (pre.classList.contains("revealed")) { // second click re-hides
      mask(card);
      return;
    }
    try {
      await window.gypsumSecure.requireKey();
      var plain = await window.gypsumSecure.decrypt(ct, card.dataset.variant);
      pre.textContent = plain;
      pre.classList.add("revealed");
      revealTimers.set(card, setTimeout(function () { mask(card); }, HOLD_MS));
    } catch (err) {
      console.error("secret reveal failed:", err);
    }
  }

  async function copySecret(card, btn) {
    var ct = card.dataset.ciphertext;
    if (!ct) return;
    try {
      await window.gypsumSecure.requireKey();
      var plain = await window.gypsumSecure.decrypt(ct, card.dataset.variant);
      await navigator.clipboard.writeText(plain);
      btn.textContent = "✓";
      setTimeout(function () { btn.textContent = COPY_ICON; }, 1500);
    } catch (err) {
      console.error("secret copy failed:", err);
    }
  }

  // Re-mask everything when the key is dropped or the tab is hidden, so a
  // revealed secret is never left on an unattended screen.
  function maskAll() {
    list.querySelectorAll(".secret-card").forEach(mask);
  }
  if (window.gypsumSecure && window.gypsumSecure.onChange) {
    window.gypsumSecure.onChange(function (unlocked) {
      if (!unlocked) maskAll();
    });
  }
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") maskAll();
  });

  // ── Card rendering ───────────────────────────────────────────────────

  // syncCard applies a card's dataset (title, url, description, image) to its
  // rendered elements, so create and edit share one rendering path.
  function syncCard(card) {
    var title = card.dataset.title || "Untitled";
    card.dataset.search = [title, card.dataset.description || "", card.dataset.url || ""].join(" ");

    card.querySelector(".secret-title").textContent = title;

    var tile = card.querySelector(".secret-tile");
    for (var i = 0; i < HUES; i++) tile.classList.remove("secret-color-" + i);
    tile.classList.add("secret-color-" + secretColor(title));
    tile.textContent = "";
    if (card.dataset.image) {
      var img = document.createElement("img");
      img.className = "secret-tile-img";
      img.src = "/images/" + card.dataset.image;
      img.alt = "";
      img.loading = "lazy";
      tile.appendChild(img);
    } else {
      var span = document.createElement("span");
      span.className = "secret-mnemonic";
      span.textContent = secretMnemonic(title);
      tile.appendChild(span);
    }

    var head = card.querySelector(".secret-head");
    var link = card.querySelector(".secret-link");
    if (card.dataset.url) {
      if (!link) {
        link = document.createElement("a");
        link.className = "secret-link";
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.textContent = "Link";
        head.insertBefore(link, head.querySelector(".secret-actions"));
      }
      link.href = card.dataset.url;
    } else if (link) {
      link.remove();
    }

    var desc = card.querySelector(".secret-desc");
    if (card.dataset.description) {
      if (!desc) {
        desc = document.createElement("p");
        desc.className = "secret-desc";
        card.querySelector(".secret-main").appendChild(desc);
      }
      desc.textContent = card.dataset.description;
    } else if (desc) {
      desc.remove();
    }
  }

  // buildCard creates an empty card shell; syncCard fills it in.
  function buildCard(id) {
    var card = document.createElement("div");
    card.className = "secret-card";
    card.dataset.id = id;

    var tile = document.createElement("div");
    tile.className = "secret-tile";

    var main = document.createElement("div");
    main.className = "secret-main";

    var head = document.createElement("div");
    head.className = "secret-head";
    var h2 = document.createElement("h2");
    h2.className = "secret-title";
    head.appendChild(h2);

    var actions = document.createElement("div");
    actions.className = "secret-actions";
    [
      ["secret-reveal", "👁", "Reveal", "Reveal secret"],
      ["secret-copy", COPY_ICON, "Copy to clipboard", "Copy secret"],
      ["secret-edit", "✎", "Edit", "Edit secret"],
      ["secret-delete", "🗑", "Delete", "Delete secret"],
    ].forEach(function (spec) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "secret-btn " + spec[0];
      b.textContent = spec[1];
      b.title = spec[2];
      b.setAttribute("aria-label", spec[3]);
      actions.appendChild(b);
    });
    head.appendChild(actions);

    var pre = document.createElement("pre");
    pre.className = "secret-value";
    pre.tabIndex = 0;
    pre.setAttribute("role", "button");
    pre.setAttribute("aria-label", "Secret value, click to reveal");
    pre.textContent = MASK;

    main.appendChild(head);
    main.appendChild(pre);
    card.appendChild(tile);
    card.appendChild(main);
    return card;
  }

  // insertSorted places a card in the list by title, matching the server's
  // case-insensitive title order so a reload keeps the same sequence.
  function insertSorted(card) {
    var title = (card.dataset.title || "").toLowerCase();
    var siblings = list.querySelectorAll(".secret-card");
    for (var i = 0; i < siblings.length; i++) {
      if (siblings[i] === card) continue;
      if ((siblings[i].dataset.title || "").toLowerCase() > title) {
        list.insertBefore(card, siblings[i]);
        return;
      }
    }
    list.appendChild(card);
  }

  // ── Add / edit dialog ────────────────────────────────────────────────

  var dialog = document.getElementById("secret-dialog");
  var form = document.getElementById("secret-form");
  var dialogTitle = document.getElementById("secret-dialog-title");
  var fTitle = document.getElementById("secret-field-title");
  var fSecret = document.getElementById("secret-field-secret");
  var fURL = document.getElementById("secret-field-url");
  var fDesc = document.getElementById("secret-field-description");
  var fImage = document.getElementById("secret-field-image");
  var imagePreview = document.getElementById("secret-image-preview");
  var secretHint = document.getElementById("secret-secret-hint");
  var errorEl = document.getElementById("secret-error");
  var submitBtn = document.getElementById("secret-submit");

  var editingCard = null;   // null → creating a new secret
  var originalSecret = null; // prefilled plaintext, to detect "unchanged"

  function setError(msg) {
    errorEl.textContent = msg || "";
    errorEl.hidden = !msg;
  }

  function setHint(msg) {
    secretHint.textContent = msg || "";
    secretHint.hidden = !msg;
  }

  function refreshImagePreview() {
    imagePreview.textContent = "";
    if (!fImage.value.trim()) return;
    var img = document.createElement("img");
    img.src = "/images/" + fImage.value.trim();
    img.alt = "";
    imagePreview.appendChild(img);
  }

  function openDialog(card) {
    editingCard = card || null;
    originalSecret = null;
    setError("");
    form.reset();
    dialogTitle.textContent = card ? "Edit secret" : "New secret";
    submitBtn.disabled = false;
    submitBtn.textContent = "Save";

    if (card) {
      fTitle.value = card.dataset.title || "";
      fURL.value = card.dataset.url || "";
      fDesc.value = card.dataset.description || "";
      fImage.value = card.dataset.image || "";
      // Prefill the plaintext only when the vault is already unlocked —
      // editing a description should not force a passphrase prompt.
      if (window.gypsumSecure.isUnlocked() && card.dataset.ciphertext) {
        setHint("Decrypting…");
        window.gypsumSecure.decrypt(card.dataset.ciphertext, card.dataset.variant).then(
          function (plain) { fSecret.value = plain; originalSecret = plain; setHint(""); },
          function () { setHint("This secret could not be decrypted with your key. Leave blank to keep it unchanged."); }
        );
      } else {
        setHint("Locked — leave blank to keep the stored secret unchanged.");
      }
    } else {
      setHint("");
    }
    refreshImagePreview();

    dialog.hidden = false;
    setTimeout(function () { fTitle.focus(); }, 0);
  }

  function closeDialog() {
    dialog.hidden = true;
    editingCard = null;
    originalSecret = null;
    form.reset();
    setError("");
    setHint("");
    imagePreview.textContent = "";
  }

  document.getElementById("secret-add-btn").addEventListener("click", function () { openDialog(null); });
  document.getElementById("secret-dialog-close").addEventListener("click", closeDialog);
  document.getElementById("secret-cancel").addEventListener("click", closeDialog);
  dialog.addEventListener("click", function (e) {
    if (e.target === dialog) closeDialog();
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && !dialog.hidden) closeDialog();
  });
  fImage.addEventListener("change", refreshImagePreview);

  // "From site" fetches the site's own image for an already-saved secret. A
  // new secret has no id yet, so its image is fetched right after creation.
  document.getElementById("secret-image-fetch").addEventListener("click", function () {
    if (!editingCard) {
      setError("Save the secret first — the image is fetched from its URL.");
      return;
    }
    if (!fURL.value.trim()) {
      setError("Add a URL to fetch an image from.");
      return;
    }
    setError("");
    var btn = this;
    btn.disabled = true;
    fetchSiteImage(editingCard.dataset.id).then(function (image) {
      btn.disabled = false;
      if (!image) {
        setError("No image found for that site.");
        return;
      }
      fImage.value = image;
      refreshImagePreview();
      editingCard.dataset.image = image;
      syncCard(editingCard);
    });
  });

  // fetchSiteImage asks the server to download the site's picture. It resolves
  // to the stored filename, or null when the site offers nothing usable.
  function fetchSiteImage(id) {
    return fetch("/secrets/image/" + id, { method: "POST" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) { return data && data.ok ? data.image : null; })
      .catch(function () { return null; });
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    setError("");

    var title = fTitle.value.trim();
    if (!title) {
      setError("A title is required.");
      return;
    }
    var plaintext = fSecret.value;
    if (!editingCard && !plaintext.trim()) {
      setError("A secret value is required.");
      return;
    }

    // An unchanged secret is sent as empty so the server keeps the stored
    // ciphertext: re-encrypting would change the nonce and dirty the git diff.
    var body = new URLSearchParams();
    body.set("title", title);
    body.set("url", fURL.value.trim());
    body.set("description", fDesc.value.trim());
    body.set("image", fImage.value.trim());

    var reEncrypt = !editingCard || (plaintext !== originalSecret && plaintext.trim() !== "");
    if (reEncrypt) {
      try {
        await window.gypsumSecure.requireKey();
        var ct = await window.gypsumSecure.encrypt(plaintext);
        body.set("secret", "{{" + window.gypsumSecure.activeMacro() + ":" + ct + "}}");
      } catch (err) {
        setError("Unlock required to encrypt this secret.");
        return;
      }
    }

    submitBtn.disabled = true;
    submitBtn.textContent = "Saving…";
    try {
      var url = editingCard ? "/secrets/save/" + editingCard.dataset.id : "/secrets/create";
      var resp = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
      });
      if (!resp.ok) {
        setError((await resp.text()).trim() || "Save failed.");
        return;
      }
      var data = await resp.json();
      await applySaved(data, body.get("secret"));
      closeDialog();
    } catch (err) {
      setError("Save failed: " + err.message);
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "Save";
    }
  });

  // applySaved updates (or creates) the card for a saved secret and kicks off
  // the site-image fetch when the entry has a URL but no image yet.
  async function applySaved(data, macro) {
    var card = editingCard;
    if (!card) {
      card = buildCard(data.id);
      list.appendChild(card);
    }
    card.dataset.title = data.title;
    card.dataset.url = fURL.value.trim();
    card.dataset.description = fDesc.value.trim();
    card.dataset.image = data.image || "";
    if (macro) {
      var m = /^\{\{secure_aes(2?):([A-Za-z0-9+/=]+)\}\}$/.exec(macro);
      if (m) {
        card.dataset.variant = m[1];
        card.dataset.ciphertext = m[2];
      }
    }
    mask(card);
    syncCard(card);
    insertSorted(card);
    applyFilter();

    if (!card.dataset.image && card.dataset.url) {
      var image = await fetchSiteImage(card.dataset.id);
      if (image) {
        card.dataset.image = image;
        syncCard(card);
      }
    }
  }

  // ── Card actions ─────────────────────────────────────────────────────

  list.addEventListener("click", function (e) {
    var card = e.target.closest(".secret-card");
    if (!card) return;
    var btn = e.target.closest(".secret-btn");

    if (!btn) {
      if (e.target.closest(".secret-value")) reveal(card);
      return;
    }
    if (btn.classList.contains("secret-reveal")) {
      reveal(card);
    } else if (btn.classList.contains("secret-copy")) {
      copySecret(card, btn);
    } else if (btn.classList.contains("secret-edit")) {
      openDialog(card);
    } else if (btn.classList.contains("secret-delete")) {
      if (!window.confirm("Delete “" + (card.dataset.title || "this secret") + "” permanently?")) return;
      fetch("/secrets/delete/" + card.dataset.id, { method: "POST" }).then(function (r) {
        if (r.ok) {
          card.remove();
          applyFilter();
        }
      });
    }
  });

  // Keyboard: the masked value behaves like a button.
  list.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" && e.key !== " ") return;
    var pre = e.target.closest(".secret-value");
    if (!pre) return;
    e.preventDefault();
    reveal(pre.closest(".secret-card"));
  });

  applyFilter();
})();
