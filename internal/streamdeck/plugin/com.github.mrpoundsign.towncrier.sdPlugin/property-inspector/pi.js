"use strict";
(() => {
  // src/pi.ts
  var PropertyInspector = class {
    ws = null;
    uuid = "";
    actionInfo = null;
    settings = {};
    globalSettings = {
      host: "127.0.0.1",
      port: 8765,
      token: ""
    };
    connect(inPort, inUUID, inRegisterEvent, _inInfo, inActionInfo) {
      this.uuid = inUUID;
      try {
        this.actionInfo = JSON.parse(inActionInfo);
        this.settings = this.actionInfo.payload?.settings ?? {};
      } catch {
        this.settings = {};
      }
      this.renderActionSections();
      this.populateFields();
      this.bindInputListeners();
      this.ws = new WebSocket(`ws://127.0.0.1:${inPort}`);
      this.ws.onopen = () => {
        this.send({
          event: inRegisterEvent,
          uuid: this.uuid
        });
        this.send({
          event: "getGlobalSettings",
          context: this.uuid
        });
      };
      this.ws.onmessage = (evt) => {
        try {
          const msg = JSON.parse(String(evt.data));
          if (msg.event === "didReceiveGlobalSettings") {
            this.globalSettings = {
              ...this.globalSettings,
              ...msg.payload?.settings
            };
            this.populateGlobalFields();
            if (this.actionInfo?.action === "com.github.mrpoundsign.towncrier.action") {
              void this.refreshActionCatalog();
            }
          } else if (msg.event === "didReceiveSettings") {
            this.settings = msg.payload?.settings ?? {};
            this.populateFields();
          }
        } catch {
        }
      };
    }
    send(msg) {
      if (this.ws && this.ws.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify(msg));
      }
    }
    renderActionSections() {
      const action = this.actionInfo?.action ?? "";
      const sections = document.querySelectorAll("[data-action]");
      sections.forEach((sec) => {
        sec.style.display = sec.dataset.action === action ? "block" : "none";
      });
    }
    populateGlobalFields() {
      const hostEl = document.getElementById("tc-host");
      const portEl = document.getElementById("tc-port");
      const tokenEl = document.getElementById("tc-token");
      if (hostEl) hostEl.value = this.globalSettings.host ?? "127.0.0.1";
      if (portEl) portEl.value = String(this.globalSettings.port ?? 8765);
      if (tokenEl) tokenEl.value = this.globalSettings.token ?? "";
    }
    populateFields() {
      const inputs = document.querySelectorAll("[data-setting]");
      inputs.forEach((el) => {
        const key = el.dataset.setting;
        if (key && this.settings[key] !== void 0 && document.activeElement !== el) {
          el.value = String(this.settings[key]);
        }
      });
    }
    persistSettings() {
      this.send({
        event: "setSettings",
        context: this.uuid,
        payload: this.settings
      });
    }
    async refreshActionCatalog() {
      const picker = document.getElementById("tc-action-picker");
      if (!picker) return;
      const host = this.globalSettings.host || "127.0.0.1";
      const port = this.globalSettings.port || 8765;
      const token = this.globalSettings.token || "";
      const headers = {};
      if (token) {
        headers.Authorization = `Bearer ${token}`;
      }
      picker.innerHTML = '<option value="">Loading live actions...</option>';
      try {
        const resp = await fetch(`http://${host}:${port}/api/actions`, { headers });
        if (!resp.ok) {
          picker.innerHTML = `<option value="">TownCrier API error (HTTP ${resp.status})</option>`;
          return;
        }
        const cat = await resp.json();
        picker.innerHTML = '<option value="">-- Select a Bot or Plugin action --</option>';
        for (const pa of cat.plugin_actions ?? []) {
          const opt = document.createElement("option");
          opt.value = JSON.stringify({
            targetType: "plugin",
            pluginUuid: pa.plugin_uuid,
            actionId: pa.action_id,
            label: pa.action_name
          });
          opt.textContent = `[${pa.plugin_name}] ${pa.action_name} (${pa.action_id})`;
          picker.appendChild(opt);
        }
        for (const ba of cat.bot_actions ?? []) {
          const opt = document.createElement("option");
          opt.value = JSON.stringify({
            targetType: "bot",
            pluginUuid: "",
            actionId: ba.id,
            label: ba.name || ba.id
          });
          opt.textContent = `[Bot] ${ba.name || ba.id}`;
          picker.appendChild(opt);
        }
      } catch {
        picker.innerHTML = '<option value="">TownCrier unreachable \u2014 verify host/port</option>';
      }
    }
    bindInputListeners() {
      const inputs = document.querySelectorAll("[data-setting]");
      inputs.forEach((el) => {
        const persist = () => {
          const key = el.dataset.setting;
          if (!key) return;
          this.settings[key] = el.type === "number" ? Number(el.value) : el.value;
          this.persistSettings();
        };
        el.addEventListener("input", persist);
        el.addEventListener("change", persist);
      });
      const picker = document.getElementById("tc-action-picker");
      picker?.addEventListener("change", () => {
        if (!picker.value) return;
        try {
          const picked = JSON.parse(picker.value);
          this.settings.targetType = picked.targetType;
          this.settings.pluginUuid = picked.pluginUuid;
          this.settings.actionId = picked.actionId;
          this.settings.label = picked.label;
          this.populateFields();
          this.persistSettings();
        } catch {
        }
      });
      const refreshBtn = document.getElementById("tc-refresh-actions");
      refreshBtn?.addEventListener("click", () => {
        void this.refreshActionCatalog();
      });
      const saveGlobal = () => {
        const hostEl = document.getElementById("tc-host");
        const portEl = document.getElementById("tc-port");
        const tokenEl = document.getElementById("tc-token");
        this.globalSettings = {
          host: hostEl?.value || "127.0.0.1",
          port: Number(portEl?.value || 8765),
          token: tokenEl?.value || ""
        };
        this.send({
          event: "setGlobalSettings",
          context: this.uuid,
          payload: this.globalSettings
        });
      };
      ["tc-host", "tc-port", "tc-token"].forEach((id) => {
        const el = document.getElementById(id);
        el?.addEventListener("input", saveGlobal);
        el?.addEventListener("change", saveGlobal);
      });
    }
  };
  var pi = new PropertyInspector();
  window.connectElgatoStreamDeckSocket = (inPort, inUUID, inRegisterEvent, inInfo, inActionInfo) => {
    pi.connect(inPort, inUUID, inRegisterEvent, inInfo, inActionInfo);
  };
})();
