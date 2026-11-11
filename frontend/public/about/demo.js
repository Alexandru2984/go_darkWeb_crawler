const samples = [
  {
    id: "archive",
    title: "Open research archive",
    category: "Research",
    description:
      "A searchable collection of research papers and public-interest reports.",
    address: "archive.example.invalid",
    note: "Check the publication dates and keep a note of relevant sources.",
  },
  {
    id: "docs",
    title: "Project documentation",
    category: "Documentation",
    description:
      "Guides covering project setup, account security and data retention.",
    address: "docs.example.invalid",
    note: "Review the security guide before sharing findings with the team.",
  },
  {
    id: "community",
    title: "Community discussion",
    category: "Community",
    description: "Conversations about privacy tools and open-source projects.",
    address: "community.example.invalid",
    note: "Follow the project announcement thread for future updates.",
  },
];
const watched = new Set();
const query = document.querySelector("#demo-query");
const category = document.querySelector("#demo-category");
const records = document.querySelector("#demo-records");
const summary = document.querySelector("#demo-summary");
const reset = document.querySelector("#demo-reset");
const template = document.querySelector("#record-template");

function render() {
  const term = query.value.trim().toLowerCase();
  const matches = samples.filter(
    (record) =>
      (!category.value || record.category === category.value) &&
      `${record.title} ${record.description} ${record.category}`
        .toLowerCase()
        .includes(term),
  );
  records.replaceChildren();
  for (const sample of matches) {
    const fragment = template.content.cloneNode(true);
    fragment.querySelector(".category").textContent = sample.category;
    fragment.querySelector("h3").textContent = sample.title;
    fragment.querySelector(".record-description").textContent =
      sample.description;
    fragment.querySelector(".record-address").textContent = sample.address;
    fragment.querySelector(".sample-note").textContent = sample.note;
    const button = fragment.querySelector(".watch-button");
    button.setAttribute("aria-pressed", String(watched.has(sample.id)));
    button.textContent = watched.has(sample.id)
      ? "Watching changes"
      : "Watch changes";
    button.setAttribute(
      "aria-label",
      `${button.textContent} for ${sample.title}`,
    );
    button.addEventListener("click", () => {
      if (watched.has(sample.id)) watched.delete(sample.id);
      else watched.add(sample.id);
      const active = watched.has(sample.id);
      button.setAttribute("aria-pressed", String(active));
      button.textContent = active ? "Watching changes" : "Watch changes";
      button.setAttribute(
        "aria-label",
        `${button.textContent} for ${sample.title}`,
      );
      updateSummary(matches.length);
    });
    records.appendChild(fragment);
  }
  updateSummary(matches.length);
}

function updateSummary(count) {
  summary.textContent = `${count} sample record${count === 1 ? "" : "s"} · ${watched.size} watched`;
}

query.addEventListener("input", render);
category.addEventListener("change", render);
reset.addEventListener("click", () => {
  watched.clear();
  query.value = "";
  category.value = "";
  render();
});
reset.hidden = false;
render();
