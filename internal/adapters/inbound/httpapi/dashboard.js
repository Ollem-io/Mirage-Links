/* Same-origin fragment driver: errors remain visible and controls are never silently ignored. */
(() => {
  const csrf = () => (document.cookie.match(/(?:^|; )mirage_dashboard_csrf=([^;]+)/) || [])[1] || "";
  const notices = () => document.querySelector("#notices");
  const message = (text, error = true) => `<div role="alert" class="alert ${error ? "alert-error" : "alert-success"}"><span>${text}</span></div>`;
  const setBusy = (el, busy) => {
    if (!el) return;
    el.setAttribute("aria-busy", String(busy));
    el.querySelectorAll("button, input").forEach(control => { control.disabled = busy; });
    const button = el.matches("button") ? el : el.querySelector("button[type=submit]");
    if (button) button.classList.toggle("loading", busy);
  };
  async function run(el, method, url, body) {
    const target = document.querySelector(el.getAttribute("hx-target")) || notices();
    setBusy(el, true);
    try {
      const response = await fetch(url, { method, body, credentials: "same-origin", headers: { "HX-Request": "true", "X-Mirage-CSRF": csrf() } });
      const redirect = response.headers.get("HX-Redirect");
      if (redirect) { location.assign(redirect); return; }
      const html = await response.text();
      if (!response.ok) {
        if (target) target.innerHTML = html || message("Request failed.");
        return;
      }
      if (target) {
        target.innerHTML = html || message("Action completed.", false);
        target.querySelector("input, button, a")?.focus();
      } else if (html) {
        notices()?.insertAdjacentHTML("beforeend", html);
      }
    } catch (_) {
      if (target) target.innerHTML = message("Network error. Please try again.");
    } finally { setBusy(el, false); }
  }
  document.addEventListener("submit", event => {
    const form = event.target;
    const attr = ["hx-post", "hx-delete"].find(name => form.hasAttribute(name));
    if (!attr) return;
    event.preventDefault(); run(form, attr.slice(3).toUpperCase(), form.getAttribute(attr), new FormData(form));
  });
  document.addEventListener("click", event => {
    const trigger = event.target.closest("[hx-get]");
    if (trigger) { event.preventDefault(); run(trigger, "GET", trigger.getAttribute("hx-get")); return; }
    const action = event.target.closest("[data-confirm-action]");
    if (!action) return;
    const dialog = document.querySelector("#dashboard-confirm");
    const form = document.querySelector("#confirm-form");
    if (!dialog || !form) return;
    event.preventDefault();
    form.dataset.method = action.dataset.confirmAction === "delete-link" ? "DELETE" : "POST";
    form.dataset.target = action.dataset.confirmTarget;
    document.querySelector("#confirm-title").textContent = `Confirm ${action.dataset.confirmAction.replaceAll("-", " ")}`;
    document.querySelector("#confirm-description").textContent = `This will affect ${action.dataset.confirmName}. Provide an audit reason to continue.`;
    form.querySelector("#confirm-reason").value = "";
    dialog.showModal(); form.querySelector("#confirm-reason").focus();
  });
  document.querySelector("[data-confirm-cancel]")?.addEventListener("click", () => document.querySelector("#dashboard-confirm")?.close());
  document.querySelector("#confirm-form")?.addEventListener("submit", event => {
    event.preventDefault(); const form = event.currentTarget; const dialog = document.querySelector("#dashboard-confirm");
    const proxy = document.createElement("form"); proxy.setAttribute("hx-target", form.dataset.target.includes("/admin/") ? "#notices" : "#links");
    run(proxy, form.dataset.method, form.dataset.target, new FormData(form)); dialog?.close();
  });
  addEventListener("DOMContentLoaded", () => document.querySelectorAll('[hx-trigger="load"]').forEach(el => run(el, "GET", el.getAttribute("hx-get"))));
})();
