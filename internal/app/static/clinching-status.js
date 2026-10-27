// Run while parsing the disclosure, before its status panels can be painted.
const statusDisclosure = document.currentScript.parentElement;
const statusMobile = window.matchMedia("(max-width: 700px)");
let statusChosen = false;
statusDisclosure.open = !statusMobile.matches;
statusDisclosure.querySelector("summary").addEventListener("click", () => { statusChosen = true; });
statusMobile.addEventListener("change", () => {
  if (!statusChosen) statusDisclosure.open = !statusMobile.matches;
});
