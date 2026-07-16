// Quick Notes board: always-editable sticky notes with debounced autosave and
// stable title-hashed colors. All state lives in the DOM (data-id, data-ts,
// data-archived), so this file needs no server-injected template data.
(function () {
  "use strict";

  var grid = document.getElementById("notes-grid");
  if (!grid) return;

  var archivedBoard = grid.dataset.archived === "1";
  var SAVE_DELAY = 2000;
  var COLORS = 8;
  var timers = new WeakMap();
  var enc = new TextEncoder();

  // noteColor mirrors NoteColor in notes.go exactly: FNV-1a over the UTF-8
  // bytes of the normalized title (trim, strip leading '#', trim, lowercase).
  // Keep this in sync with the Go implementation and its unit tests.
  function noteColor(title) {
    var norm = title.trim().replace(/^#+/, "").trim().toLowerCase();
    var bytes = enc.encode(norm);
    var h = 0x811c9dc5;
    for (var i = 0; i < bytes.length; i++) {
      h ^= bytes[i];
      h = Math.imul(h, 0x01000193) >>> 0;
    }
    return h % COLORS;
  }

  function firstLine(text) {
    var lines = text.split("\n");
    for (var i = 0; i < lines.length; i++) {
      var t = lines[i].trim();
      if (t) return t;
    }
    return "";
  }

  function autoGrow(ta) {
    ta.style.height = "auto";
    ta.style.height = ta.scrollHeight + "px";
  }

  function recolor(card, text) {
    var c = noteColor(firstLine(text));
    for (var i = 0; i < COLORS; i++) card.classList.remove("note-color-" + i);
    card.classList.add("note-color-" + c);
  }

  function relTime(ts) {
    if (!ts) return "";
    var diff = Math.floor(Date.now() / 1000) - ts;
    if (diff < 5) return "just now";
    if (diff < 60) return diff + "s ago";
    if (diff < 3600) return Math.floor(diff / 60) + "m ago";
    if (diff < 86400) return Math.floor(diff / 3600) + "h ago";
    return Math.floor(diff / 86400) + "d ago";
  }

  function refreshTimes() {
    grid.querySelectorAll(".note-updated").forEach(function (s) {
      s.textContent = relTime(parseInt(s.dataset.ts || "0", 10));
    });
  }

  function markSaved(card) {
    var span = card.querySelector(".note-updated");
    if (span) {
      span.dataset.ts = String(Math.floor(Date.now() / 1000));
      span.textContent = "just now";
    }
  }

  var post = function (url, content) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ content: content }),
      keepalive: true,
    });
  };

  function saveCard(card) {
    var ta = card.querySelector(".note-text");
    if (!ta) return;
    var content = ta.value;
    var id = card.dataset.id;

    if (!id) {
      // New ghost card: only persist once it has real text.
      if (!content.trim()) return;
      if (card.dataset.creating === "1") {
        card.dataset.pending = "1"; // a save arrived mid-create; run it after
        return;
      }
      card.dataset.creating = "1";
      post("/notes/create", content)
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          card.dataset.creating = "";
          if (!data) return;
          card.dataset.id = data.id;
          card.classList.remove("note-new");
          delete card.dataset.new;
          buildFooter(card);
          markSaved(card);
          ensureGhost();
          if (card.dataset.pending === "1") {
            card.dataset.pending = "";
            scheduleSave(card);
          }
        })
        .catch(function () { card.dataset.creating = ""; });
      return;
    }

    post("/notes/save/" + id, content).then(function (r) {
      if (r.ok) markSaved(card);
    });
  }

  function scheduleSave(card) {
    clearTimeout(timers.get(card));
    timers.set(card, setTimeout(function () { saveCard(card); }, SAVE_DELAY));
  }

  function flush(card) {
    if (timers.has(card)) {
      clearTimeout(timers.get(card));
      timers.delete(card);
      saveCard(card);
    }
  }

  // buildFooter adds the timestamp + action buttons to a card that was just
  // promoted from a ghost card to a saved note.
  function buildFooter(card) {
    if (card.querySelector(".note-footer")) return;
    var footer = document.createElement("div");
    footer.className = "note-footer";
    var updated = document.createElement("span");
    updated.className = "note-updated";
    updated.dataset.ts = "0";
    var actions = document.createElement("span");
    actions.className = "note-actions";
    var archive = document.createElement("button");
    archive.type = "button";
    archive.className = "note-btn note-archive";
    archive.title = "Archive note";
    archive.textContent = "archive";
    var del = document.createElement("button");
    del.type = "button";
    del.className = "note-btn note-delete";
    del.title = "Delete permanently";
    del.textContent = "delete";
    actions.appendChild(archive);
    actions.appendChild(del);
    footer.appendChild(updated);
    footer.appendChild(actions);
    card.appendChild(footer);
  }

  // ensureGhost guarantees there is exactly one trailing empty ghost card on
  // the active board so a new note can always be started.
  function ensureGhost() {
    if (archivedBoard) return;
    if (grid.querySelector(".note-card.note-new")) return;
    var card = document.createElement("div");
    card.className = "note-card note-new";
    card.dataset.new = "1";
    var ta = document.createElement("textarea");
    ta.className = "note-text";
    ta.spellcheck = false;
    ta.placeholder = "New note…";
    card.appendChild(ta);
    grid.appendChild(card);
    wireTextarea(ta);
  }

  function wireTextarea(ta) {
    autoGrow(ta);
    if (archivedBoard || ta.readOnly) return;
    ta.addEventListener("input", function () {
      var card = ta.closest(".note-card");
      autoGrow(ta);
      recolor(card, ta.value);
      scheduleSave(card);
    });
    ta.addEventListener("blur", function () {
      flush(ta.closest(".note-card"));
    });
  }

  // Action buttons (archive / restore / delete) via event delegation.
  grid.addEventListener("click", function (e) {
    var btn = e.target.closest(".note-btn");
    if (!btn) return;
    var card = btn.closest(".note-card");
    var id = card && card.dataset.id;
    if (!id) return;

    if (btn.classList.contains("note-delete")) {
      if (!window.confirm("Delete this note permanently?")) return;
      fetch("/notes/delete/" + id, { method: "POST" }).then(function (r) {
        if (r.ok) card.remove();
      });
      return;
    }
    var action = btn.classList.contains("note-restore") ? "restore" : "archive";
    fetch("/notes/" + action + "/" + id, { method: "POST" }).then(function (r) {
      if (r.ok) card.remove();
    });
  });

  // Flush pending edits when the tab is hidden or the page is being unloaded.
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") {
      grid.querySelectorAll(".note-card").forEach(flush);
    }
  });

  // Initial wiring.
  grid.querySelectorAll(".note-text").forEach(wireTextarea);
  refreshTimes();
  setInterval(refreshTimes, 60000);
})();
