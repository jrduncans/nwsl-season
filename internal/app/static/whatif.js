document.addEventListener("change", (event) => {
  const form = event.target.closest("form[data-auto-submit]");
  if (!form || event.target.tagName !== "SELECT") return;
  form.requestSubmit();
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-standings-mode-button]");
  if (!button) return;

  const display = button.closest("[data-standings]");
  const mode = button.dataset.standingsModeValue;
  if (!display || !["per-game", "total"].includes(mode) || display.dataset.standingsMode === mode) return;

  display.classList.add("is-switching");
  requestAnimationFrame(() => {
    display.dataset.standingsMode = mode;
    sortStandings(display, mode);
    display.querySelectorAll("[data-standings-mode-button]").forEach((candidate) => {
      candidate.setAttribute("aria-pressed", String(candidate === button));
    });
    display.querySelectorAll("[data-standings-value]").forEach((value) => {
      value.textContent = value.parentElement.dataset[mode === "per-game" ? "perGame" : "total"];
    });
    requestAnimationFrame(() => display.classList.remove("is-switching"));
  });
});

function sortStandings(display, mode) {
  const tbody = display.querySelector("[data-standings-table] tbody");
  if (!tbody) return;

  const dataKey = mode === "per-game" ? "perGame" : "total";
  const rows = Array.from(tbody.rows);
  const startingPositions = new Map(rows.map((row) => [row, row.getBoundingClientRect().top]));
  rows.sort((left, right) => Number(left.querySelector("[data-standings-position]").dataset[dataKey]) - Number(right.querySelector("[data-standings-position]").dataset[dataKey]));
  rows.forEach((row) => tbody.append(row));
  rows.forEach((row) => {
    const position = row.querySelector("[data-standings-position]");
    position.textContent = position.dataset[dataKey];
    row.classList.toggle("playoff-line", row.dataset[`${dataKey}PlayoffLine`] === "true");

    const offset = startingPositions.get(row) - row.getBoundingClientRect().top;
    if (!offset || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    row.style.transition = "none";
    row.style.transform = `translateY(${offset}px)`;
    requestAnimationFrame(() => {
      row.style.transition = "transform .18s ease";
      row.style.transform = "";
    });
  });
}
