document.addEventListener("change", (event) => {
  const form = event.target.closest("form[data-auto-submit]");
  if (!form || event.target.tagName !== "SELECT") return;
  form.requestSubmit();
});
