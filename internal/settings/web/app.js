async function load() {
  const res = await fetch("/api/v1/settings");
  if (!res.ok) throw new Error("Failed to load settings");
  const data = await res.json();
  document.getElementById("probe").textContent = data.summary || JSON.stringify(data.probe, null, 2);
  const provider = (data.config && data.config.ai && data.config.ai.reasoning && data.config.ai.reasoning.provider) || "hybrid";
  const radio = document.querySelector(`input[name="ai_mode"][value="${provider}"]`);
  if (radio) radio.checked = true;
  if (data.config) {
    document.getElementById("discovery_root").value = data.config.discovery_root || "";
    document.getElementById("logs_root").value = data.config.logs_root || "";
    const r = data.config.ai && data.config.ai.reasoning;
    if (r && r.routing) {
      document.getElementById("use_cloud_when_no_gpu").checked = !!r.routing.use_cloud_when_no_gpu;
    }
    if (r && r.always_smallest_local) {
      document.querySelector('input[name="local_model"][value="granite4.1:3b"]').checked = true;
    }
    if (r && r.clouds && r.clouds.openai_compat) {
      document.getElementById("openai_base").value = r.clouds.openai_compat.base_url || "";
    }
  }
}

document.getElementById("save").addEventListener("click", async () => {
  const status = document.getElementById("status");
  status.textContent = "Saving…";
  const ai_mode = document.querySelector('input[name="ai_mode"]:checked').value;
  const local_model = document.querySelector('input[name="local_model"]:checked').value;
  const body = {
    ai_mode,
    local_model,
    always_smallest_local: local_model === "granite4.1:3b",
    use_cloud_when_no_gpu: document.getElementById("use_cloud_when_no_gpu").checked,
    gemini_key: document.getElementById("gemini_key").value,
    openai_key: document.getElementById("openai_key").value,
    openai_base_url: document.getElementById("openai_base").value,
    claude_key: document.getElementById("claude_key").value,
    discovery_root: document.getElementById("discovery_root").value,
    logs_root: document.getElementById("logs_root").value,
  };
  const res = await fetch("/api/v1/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    status.textContent = "Save failed: " + (await res.text());
    return;
  }
  status.textContent = "Saved. Restart Ibis Assistant for reasoning providers to reload.";
  document.getElementById("gemini_key").value = "";
  document.getElementById("openai_key").value = "";
  document.getElementById("claude_key").value = "";
});

load().catch((e) => {
  document.getElementById("probe").textContent = String(e);
});
