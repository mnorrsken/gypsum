// Renders the ```mermaid blocks that the Markdown renderer emits as
// <pre class="mermaid">SOURCE</pre>. The mermaid bundle is ~2.7 MB, so it is
// fetched only once a page actually contains a diagram.
(function () {
  var BUNDLE = "/static/mermaid.min.js";
  var loading = null;

  function isDark() {
    return document.documentElement.getAttribute("data-theme") === "dark";
  }

  // Mermaid backs edge labels with its own theme colour, which shows up as a
  // grey box against the page. Match the surrounding surface instead.
  function themeVariables() {
    var surface = getComputedStyle(document.documentElement)
      .getPropertyValue("--color-surface").trim();
    return surface ? { edgeLabelBackground: surface } : {};
  }

  function load() {
    if (!loading) {
      loading = new Promise(function (resolve, reject) {
        var s = document.createElement("script");
        s.src = BUNDLE;
        s.onload = function () { resolve(window.mermaid); };
        s.onerror = function () { reject(new Error("could not load " + BUNDLE)); };
        document.head.appendChild(s);
      });
    }
    return loading;
  }

  // Mermaid swaps the element's text for an SVG, so keep the source around:
  // re-rendering after a theme change needs it back.
  function unrendered() {
    var out = [];
    var els = document.querySelectorAll("pre.mermaid");
    for (var i = 0; i < els.length; i++) {
      if (els[i].dataset.mermaidSrc === undefined) {
        els[i].dataset.mermaidSrc = els[i].textContent;
      }
      if (els[i].dataset.processed !== "true") out.push(els[i]);
    }
    return out;
  }

  function render() {
    var nodes = unrendered();
    if (!nodes.length) return;
    load().then(function (mermaid) {
      mermaid.initialize({
        startOnLoad: false,
        // Author-supplied diagram text is untrusted: strict blocks script and
        // click handlers in labels, and sanitizes embedded HTML.
        securityLevel: "strict",
        theme: isDark() ? "dark" : "default",
        themeVariables: themeVariables(),
      });
      // suppressErrors keeps one malformed diagram from stopping the rest;
      // mermaid draws its own error box in the offending element.
      return mermaid.run({ nodes: nodes, suppressErrors: true });
    }).catch(function (err) {
      for (var i = 0; i < nodes.length; i++) {
        nodes[i].classList.add("mermaid-failed");
      }
      if (window.console) console.error("[gypsum] mermaid:", err);
    });
  }

  // Restore every diagram's source and draw again under the new theme.
  function rerender() {
    if (!loading) return;
    var els = document.querySelectorAll("pre.mermaid");
    for (var i = 0; i < els.length; i++) {
      if (els[i].dataset.mermaidSrc === undefined) continue;
      els[i].textContent = els[i].dataset.mermaidSrc;
      els[i].removeAttribute("data-processed");
    }
    render();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", render);
  } else {
    render();
  }

  // Content swapped in by htmx can carry diagrams of its own.
  document.addEventListener("htmx:afterSettle", render);

  new MutationObserver(rerender).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
})();
