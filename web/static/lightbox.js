// Click an image or a rendered Mermaid diagram in .wiki-content to show it
// filling the screen. Click anywhere or press Escape to close. Images wrapped
// in a link are left alone so the link still works.
(function () {
  var overlay = null;

  function close() {
    if (!overlay) return;
    overlay.remove();
    overlay = null;
    document.body.classList.remove("lightbox-open");
    document.removeEventListener("keydown", onKey);
  }

  function onKey(e) {
    if (e.key === "Escape") close();
  }

  function open(node, isDiagram) {
    close();
    overlay = document.createElement("div");
    overlay.className = "lightbox" + (isDiagram ? " lightbox-diagram" : "");
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.appendChild(node);
    overlay.addEventListener("click", close);
    document.body.appendChild(overlay);
    document.body.classList.add("lightbox-open");
    document.addEventListener("keydown", onKey);
  }

  function openImage(img) {
    var big = document.createElement("img");
    big.src = img.currentSrc || img.src;
    big.alt = img.alt;
    open(big, false);
  }

  // Mermaid sets an inline max-width and fixed size on the SVG; drop them so
  // the copy scales up to the overlay, keeping its aspect ratio via viewBox.
  function openDiagram(svg) {
    var copy = svg.cloneNode(true);
    copy.removeAttribute("style");
    copy.removeAttribute("width");
    copy.removeAttribute("height");
    var panel = document.createElement("div");
    panel.className = "lightbox-panel";
    panel.appendChild(copy);
    open(panel, true);
  }

  document.addEventListener("click", function (e) {
    var t = e.target;
    if (!(t instanceof Element) || !t.closest(".wiki-content")) return;
    if (t.tagName === "IMG" && !t.closest("a")) {
      e.preventDefault();
      openImage(t);
      return;
    }
    var pre = t.closest('pre.mermaid[data-processed="true"]');
    var svg = pre && pre.querySelector("svg");
    if (svg) {
      e.preventDefault();
      openDiagram(svg);
    }
  });
})();
