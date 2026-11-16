// Settings sections start closed (#719). This browser keeps the set of sections that the
// maintainer left open, so that they open again after an action reloads the page and on a
// later visit; a link to a section, such as #remote-access, opens it. Storage is a
// per-browser convenience: without it, every section starts closed and still opens.
(function () {
  "use strict";
  var STORE = "koinon.settings.open-sections";
  var sections = {};
  document.querySelectorAll("details.action[data-section]").forEach(function (section) {
    sections[section.dataset.section] = section;
  });
  function known(key) { return Object.prototype.hasOwnProperty.call(sections, key); }
  function saved() {
    try {
      var keys = JSON.parse(window.localStorage.getItem(STORE) || "[]");
      return Array.isArray(keys) ? keys.filter(function (key) { return typeof key === "string" && known(key); }) : [];
    } catch (e) { return []; }
  }
  function save() {
    var open = Object.keys(sections).filter(function (key) { return sections[key].open; });
    try { window.localStorage.setItem(STORE, JSON.stringify(open)); } catch (e) { /* no storage */ }
  }
  function follow() {
    var key = "";
    try { key = decodeURIComponent(window.location.hash.slice(1)); } catch (e) { return; }
    if (known(key)) { sections[key].open = true; }
  }
  saved().forEach(function (key) { sections[key].open = true; });
  follow();
  Object.keys(sections).forEach(function (key) { sections[key].addEventListener("toggle", save); });
  window.addEventListener("hashchange", follow);
})();
