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

  private bindInputListeners(): void {
    const inputs = document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-setting]');
    inputs.forEach((el) => {
      const persist = () => {
        const key = el.dataset.setting;
        if (!key) return;
        this.settings[key] = el.type === 'number' ? Number(el.value) : el.value;
        this.send({
          event: 'setSettings',
          context: this.uuid,
          payload: this.settings,
        });
      };
      el.addEventListener('input', persist);
      el.addEventListener('change', persist);
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
