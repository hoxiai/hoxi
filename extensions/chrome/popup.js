const t = key => chrome.i18n.getMessage(key);
const $ = id => document.getElementById(id);
$("hint").textContent = t("hint");
$("code").placeholder = t("code");
$("code").setAttribute("aria-label", t("code"));
$("connect").textContent = t("connect");
$("disconnect").textContent = t("disconnect");

void chrome.runtime.sendMessage({ type: "status" }).then(status => {
  if (!status) return;
  if (status.connected) {
    $("status").textContent = t("connected");
  } else if (status.hasSavedEndpoint) {
    $("connect").textContent = t("quickConnect") || t("connect");
  }
}).catch(() => {});

$("connect").onclick = async () => {
  $("connect").disabled = true;
  $("status").textContent = t("working");
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    const result = await chrome.runtime.sendMessage({ type: "connect", code: $("code").value.trim(), tabID: tab.id });
    if (result.error) throw new Error(result.error);
    $("code").value = "";
    $("connect").textContent = t("quickConnect") || t("connect");
    $("status").textContent = t("connected");
  } catch (error) { $("status").textContent = `${t("failed")}: ${error.message}`; }
  finally { $("connect").disabled = false; }
};
$("disconnect").onclick = async () => {
  await chrome.runtime.sendMessage({ type: "disconnect" });
  $("status").textContent = t("disconnected");
};
