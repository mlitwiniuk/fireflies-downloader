(() => {
  const button = document.querySelector("[data-theme-toggle]");
  if (!button) return;

  button.addEventListener("click", () => {
    const current = document.documentElement.dataset.theme || "light";
    const next = current === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    localStorage.setItem("fireflies-theme", next);
  });
})();
