// Applies the saved theme before first paint, so the page never flashes.
try {
  var t = localStorage.getItem("mm-theme")
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t
} catch (e) { /* storage blocked: follow the system theme */ }
