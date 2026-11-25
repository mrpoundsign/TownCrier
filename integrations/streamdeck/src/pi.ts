interface ActionInfo {
  action: string;
  context: string;
  payload: {
    settings?: Record<string, unknown>;
  };
}

interface GlobalSettings {
  host?: string;
  port?: number;
  token?: string;
}

interface ActionCatalogResponse {
  bot_actions?: Array<{
    id: string;
    name: string;
    enabled: boolean;
    queue_id?: string;
  }>;
  plugin_actions?: Array<{
    plugin_uuid: string;
    plugin_name: string;
    action_id: string;
    action_name: string;
    description?: string;
  }>;
}

class PropertyInspector {
  private ws: WebSocket | null = null;
  private uuid = '';
  private actionInfo: ActionInfo | null = null;
  private settings: Record<string, unknown> = {};
  private globalSettings: GlobalSettings = {
    host: '127.0.0.1',
    port: 8765,
    token: '',
  };

  public connect(inPort: string, inUUID: string, inRegisterEvent: string, _inInfo: string, inActionInfo: string): void {
    this.uuid = inUUID;
    try {
      this.actionInfo = JSON.parse(inActionInfo) as ActionInfo;
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
        uuid: this.uuid,
      });
      this.send({
        event: 'getGlobalSettings',
        context: this.uuid,
      });
    };

    this.ws.onmessage = (evt: MessageEvent) => {
      try {
        const msg = JSON.parse(String(evt.data)) as {
          event: string;
          payload?: { settings?: Record<string, unknown> };
        };
        if (msg.event === 'didReceiveGlobalSettings') {
          this.globalSettings = {
            ...this.globalSettings,
            ...(msg.payload?.settings as GlobalSettings),
          };
          this.populateGlobalFields();
          if (this.actionInfo?.action === 'com.github.mrpoundsign.towncrier.action') {
            void this.refreshActionCatalog();
          }
        } else if (msg.event === 'didReceiveSettings') {
          this.settings = msg.payload?.settings ?? {};
          this.populateFields();
        }
      } catch {
        // ignore
      }
    };
  }

  private send(msg: Record<string, unknown>): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  private renderActionSections(): void {
    const action = this.actionInfo?.action ?? '';
    const sections = document.querySelectorAll<HTMLElement>('[data-action]');
    sections.forEach((sec) => {
      sec.style.display = sec.dataset.action === action ? 'block' : 'none';
    });
  }

  private populateGlobalFields(): void {
    const hostEl = document.getElementById('tc-host') as HTMLInputElement | null;
    const portEl = document.getElementById('tc-port') as HTMLInputElement | null;
    const tokenEl = document.getElementById('tc-token') as HTMLInputElement | null;
    if (hostEl) hostEl.value = this.globalSettings.host ?? '127.0.0.1';
    if (portEl) portEl.value = String(this.globalSettings.port ?? 8765);
    if (tokenEl) tokenEl.value = this.globalSettings.token ?? '';
  }

  private populateFields(): void {
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-setting]');
    inputs.forEach((el) => {
      const key = el.dataset.setting;
      if (key && this.settings[key] !== undefined && document.activeElement !== el) {
        el.value = String(this.settings[key]);
      }
    });
  }

  private persistSettings(): void {
    this.send({
      event: 'setSettings',
      context: this.uuid,
      payload: this.settings,
    });
  }

  private async refreshActionCatalog(): Promise<void> {
    const picker = document.getElementById('tc-action-picker') as HTMLSelectElement | null;
    if (!picker) return;

    const host = this.globalSettings.host || '127.0.0.1';
    const port = this.globalSettings.port || 8765;
    const token = this.globalSettings.token || '';
    const headers: Record<string, string> = {};
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
      const cat = (await resp.json()) as ActionCatalogResponse;
      picker.innerHTML = '<option value="">-- Select a Bot or Plugin action --</option>';

      for (const pa of cat.plugin_actions ?? []) {
        const opt = document.createElement('option');
        opt.value = JSON.stringify({
          targetType: 'plugin',
          pluginUuid: pa.plugin_uuid,
          actionId: pa.action_id,
          label: pa.action_name,
        });
        opt.textContent = `[${pa.plugin_name}] ${pa.action_name} (${pa.action_id})`;
        picker.appendChild(opt);
      }

      for (const ba of cat.bot_actions ?? []) {
        const opt = document.createElement('option');
        opt.value = JSON.stringify({
          targetType: 'bot',
          pluginUuid: '',
          actionId: ba.id,
          label: ba.name || ba.id,
        });
        opt.textContent = `[Bot] ${ba.name || ba.id}`;
        picker.appendChild(opt);
      }
    } catch {
      picker.innerHTML = '<option value="">TownCrier unreachable — verify host/port</option>';
    }
  }

  private bindInputListeners(): void {
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-setting]');
    inputs.forEach((el) => {
      const persist = () => {
        const key = el.dataset.setting;
        if (!key) return;
        this.settings[key] = el.type === 'number' ? Number(el.value) : el.value;
        this.persistSettings();
      };
      el.addEventListener('input', persist);
      el.addEventListener('change', persist);
    });

    const picker = document.getElementById('tc-action-picker') as HTMLSelectElement | null;
    picker?.addEventListener('change', () => {
      if (!picker.value) return;
      try {
        const picked = JSON.parse(picker.value) as {
          targetType: string;
          pluginUuid: string;
          actionId: string;
          label: string;
        };
        this.settings.targetType = picked.targetType;
        this.settings.pluginUuid = picked.pluginUuid;
        this.settings.actionId = picked.actionId;
        this.settings.label = picked.label;
        this.populateFields();
        this.persistSettings();
      } catch {
        // ignore
      }
    });

    const refreshBtn = document.getElementById('tc-refresh-actions');
    refreshBtn?.addEventListener('click', () => {
      void this.refreshActionCatalog();
    });

    const saveGlobal = () => {
      const hostEl = document.getElementById('tc-host') as HTMLInputElement | null;
      const portEl = document.getElementById('tc-port') as HTMLInputElement | null;
      const tokenEl = document.getElementById('tc-token') as HTMLInputElement | null;
      this.globalSettings = {
        host: hostEl?.value || '127.0.0.1',
        port: Number(portEl?.value || 8765),
        token: tokenEl?.value || '',
      };
      this.send({
        event: 'setGlobalSettings',
        context: this.uuid,
        payload: this.globalSettings,
      });
    };

    ['tc-host', 'tc-port', 'tc-token'].forEach((id) => {
      const el = document.getElementById(id);
      el?.addEventListener('input', saveGlobal);
      el?.addEventListener('change', saveGlobal);
    });
  }
}

const pi = new PropertyInspector();
(window as unknown as Record<string, unknown>).connectElgatoStreamDeckSocket = (
  inPort: string,
  inUUID: string,
  inRegisterEvent: string,
  inInfo: string,
  inActionInfo: string,
) => {
  pi.connect(inPort, inUUID, inRegisterEvent, inInfo, inActionInfo);
};
