import WebSocket from 'ws';
import {
  ClientEnvelope,
  PanicAction,
  QueueAction,
  ServerEnvelope,
  StateSnapshot,
  TTSOutputMode,
  decodeServerEnvelope,
  encodeClientEnvelope,
} from './proto/control';

export interface ActionInstance {
  action: string;
  context: string;
  settings: Record<string, unknown>;
}

export interface GlobalSettings {
  host?: string;
  port?: number;
  token?: string;
  towncrierWsUrl?: string;
}

interface SDIncomingMessage {
  event: string;
  action?: string;
  context?: string;
  payload?: {
    settings?: Record<string, unknown>;
    ticks?: number;
    [key: string]: unknown;
  };
}

const ACTION_MODE = 'com.github.mrpoundsign.towncrier.mode';
const ACTION_PANIC = 'com.github.mrpoundsign.towncrier.panic';
const ACTION_QUEUE = 'com.github.mrpoundsign.towncrier.queue';
const ACTION_SPEAK = 'com.github.mrpoundsign.towncrier.speak';
const ACTION_VOLUME = 'com.github.mrpoundsign.towncrier.volume';

type IconKind = 'mode_on' | 'mode_afk' | 'mode_off' | 'panic_safe' | 'panic_muted' | 'queue_active' | 'queue_paused' | 'speak' | 'volume';

function escapeXml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

function renderIconSvg(kind: IconKind, accent: string): string {
  switch (kind) {
    case 'mode_on':
      return `<path d="M50 40 L62 40 L76 28 L76 64 L62 52 L50 52 Z" fill="${accent}"/>
              <path d="M84 35 C91 42 91 50 84 57" stroke="#F8FAFC" stroke-width="4" fill="none" stroke-linecap="round"/>
              <path d="M91 29 C102 40 102 52 91 63" stroke="${accent}" stroke-width="4" fill="none" stroke-linecap="round"/>`;
    case 'mode_afk':
      return `<circle cx="72" cy="46" r="16" stroke="${accent}" stroke-width="4" fill="none"/>
              <path d="M72 36 L72 46 L79 51" stroke="#F8FAFC" stroke-width="4" stroke-linecap="round" fill="none"/>`;
    case 'mode_off':
      return `<path d="M50 40 L62 40 L76 28 L76 64 L62 52 L50 52 Z" fill="${accent}"/>
              <line x1="84" y1="38" x2="98" y2="54" stroke="#EF4444" stroke-width="4" stroke-linecap="round"/>
              <line x1="98" y1="38" x2="84" y2="54" stroke="#EF4444" stroke-width="4" stroke-linecap="round"/>`;
    case 'panic_safe':
      return `<path d="M72 26 L92 34 L92 48 C92 59 83 66 72 70 C61 66 52 59 52 48 L52 34 Z" stroke="${accent}" stroke-width="4" fill="none"/>
              <path d="M64 47 L70 53 L81 41" stroke="#F8FAFC" stroke-width="4" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`;
    case 'panic_muted':
      return `<polygon points="62,26 82,26 96,40 96,56 82,70 62,70 48,56 48,40" fill="${accent}"/>
              <line x1="63" y1="39" x2="81" y2="57" stroke="#F8FAFC" stroke-width="5" stroke-linecap="round"/>
              <line x1="81" y1="39" x2="63" y2="57" stroke="#F8FAFC" stroke-width="5" stroke-linecap="round"/>`;
    case 'queue_active':
      return `<rect x="46" y="30" width="36" height="7" rx="3" fill="${accent}"/>
              <rect x="46" y="43" width="36" height="7" rx="3" fill="${accent}"/>
              <rect x="46" y="56" width="24" height="7" rx="3" fill="${accent}"/>
              <polygon points="88,36 102,46 88,56" fill="#F8FAFC"/>`;
    case 'queue_paused':
      return `<rect x="56" y="30" width="10" height="32" rx="3" fill="${accent}"/>
              <rect x="78" y="30" width="10" height="32" rx="3" fill="${accent}"/>`;
    case 'speak':
      return `<rect x="46" y="28" width="52" height="32" rx="8" fill="${accent}"/>
              <polygon points="62,60 72,60 64,70" fill="${accent}"/>
              <line x1="56" y1="40" x2="88" y2="40" stroke="#0F172A" stroke-width="4" stroke-linecap="round"/>
              <line x1="56" y1="49" x2="78" y2="49" stroke="#0F172A" stroke-width="4" stroke-linecap="round"/>`;
    case 'volume':
      return `<circle cx="72" cy="46" r="17" stroke="${accent}" stroke-width="5" fill="none"/>
              <line x1="72" y1="46" x2="82" y2="36" stroke="#F8FAFC" stroke-width="4" stroke-linecap="round"/>`;
  }
}

function svgDataUri(bg: string, border: string, icon: IconKind, labelTop: string, labelBottom: string, accent: string): string {
  const safeTop = escapeXml(labelTop);
  const safeBottom = escapeXml(labelBottom);
  const iconMarkup = renderIconSvg(icon, accent);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="144" height="144" viewBox="0 0 144 144">
    <rect x="4" y="4" width="136" height="136" rx="22" fill="${bg}" stroke="${border}" stroke-width="4"/>
    ${iconMarkup}
    <text x="72" y="96" font-family="Segoe UI, Arial, sans-serif" font-size="17" font-weight="700" fill="#F8FAFC" text-anchor="middle">${safeTop}</text>
    <text x="72" y="120" font-family="Segoe UI, Arial, sans-serif" font-size="15" font-weight="700" fill="${accent}" text-anchor="middle">${safeBottom}</text>
  </svg>`;
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

export class TownCrierStreamDeckPlugin {
  private sdWs: WebSocket | null = null;
  private tcWs: WebSocket | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private pluginUUID = '';
  private registerEvent = '';
  private sdPort = 0;
  private globalSettings: GlobalSettings = {
    host: '127.0.0.1',
    port: 8765,
    token: '',
  };
  private actions = new Map<string, ActionInstance>();
  private lastState: StateSnapshot = {
    ttsEnabled: true,
    outputMode: TTSOutputMode.ALWAYS_ON,
    userIdle: false,
    idleSeconds: 0,
    idleTimeoutSeconds: 15,
    speakingActive: true,
    paused: false,
    panicked: false,
    queueDepth: 0,
    volume: 0.8,
    currentVoice: '',
    currentlySpeaking: '',
  };
  private reqCounter = 0;
  private shuttingDown = false;

  public startFromArgs(argv: string[]): void {
    for (let i = 0; i < argv.length; i++) {
      const arg = argv[i];
      if (arg === '-port' && i + 1 < argv.length) {
        this.sdPort = parseInt(argv[++i] ?? '0', 10);
      } else if (arg === '-pluginUUID' && i + 1 < argv.length) {
        this.pluginUUID = argv[++i] ?? '';
      } else if (arg === '-registerEvent' && i + 1 < argv.length) {
        this.registerEvent = argv[++i] ?? '';
      } else if (arg === '-tcUrl' && i + 1 < argv.length) {
        this.globalSettings.towncrierWsUrl = argv[++i] ?? '';
      }
    }

    if (!this.sdPort || !this.pluginUUID || !this.registerEvent) {
      throw new Error('Missing required Stream Deck CLI arguments (-port, -pluginUUID, -registerEvent)');
    }

    this.connectStreamDeck();
    this.connectTownCrier();
  }

  public stop(): void {
    this.shuttingDown = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.tcWs?.close();
    this.sdWs?.close();
  }

  private connectStreamDeck(): void {
    const url = `ws://127.0.0.1:${this.sdPort}`;
    this.sdWs = new WebSocket(url);

    this.sdWs.on('open', () => {
      this.sendToSD({
        event: this.registerEvent,
        uuid: this.pluginUUID,
      });
      this.sendToSD({
        event: 'getGlobalSettings',
        context: this.pluginUUID,
      });
    });

    this.sdWs.on('message', (data: WebSocket.RawData) => {
      try {
        const text = typeof data === 'string' ? data : data.toString('utf8');
        const msg = JSON.parse(text) as SDIncomingMessage;
        this.handleStreamDeckMessage(msg);
      } catch {
        // Ignore malformed host frames
      }
    });

    this.sdWs.on('close', () => {
      if (!this.shuttingDown) {
        process.exit(0);
      }
    });
  }

  private buildTownCrierUrl(): string {
    if (this.globalSettings.towncrierWsUrl) {
      return this.globalSettings.towncrierWsUrl;
    }
    const host = this.globalSettings.host || '127.0.0.1';
    const port = this.globalSettings.port || 8765;
    const token = this.globalSettings.token || '';
    const tokenParam = token ? `&token=${encodeURIComponent(token)}` : '';
    return `ws://${host}:${port}/ws?format=protobuf${tokenParam}`;
  }

  private connectTownCrier(): void {
    if (this.shuttingDown) return;
    if (this.tcWs) {
      this.tcWs.removeAllListeners();
      try {
        this.tcWs.close();
      } catch {
        // ignore
      }
      this.tcWs = null;
    }

    const url = this.buildTownCrierUrl();
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    this.tcWs = ws;

    ws.on('open', () => {
      this.sendProto({
        requestId: this.nextReqId(),
        command: { kind: 'getState' },
      });
    });

    ws.on('message', (data: WebSocket.RawData) => {
      try {
        let bytes: Uint8Array;
        if (data instanceof ArrayBuffer) {
          bytes = new Uint8Array(data);
        } else if (ArrayBuffer.isView(data)) {
          bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
        } else if (Array.isArray(data)) {
          bytes = new Uint8Array(Buffer.concat(data));
        } else {
          bytes = new Uint8Array(data as Buffer);
        }
        const env: ServerEnvelope = decodeServerEnvelope(bytes);
        if (env.state) {
          this.lastState = env.state;
          this.refreshAllActions();
        }
      } catch {
        // Ignore malformed frames
      }
    });

    ws.on('close', () => {
      if (!this.shuttingDown) {
        this.scheduleReconnect();
      }
    });

    ws.on('error', () => {
      // Handled by close event
    });
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer || this.shuttingDown) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connectTownCrier();
    }, 2000);
  }

  private nextReqId(): string {
    this.reqCounter += 1;
    return `sd-${this.reqCounter}`;
  }

  private sendProto(env: ClientEnvelope, actionContext?: string): boolean {
    if (!this.tcWs || this.tcWs.readyState !== WebSocket.OPEN) {
      if (actionContext) {
        this.sendToSD({
          event: 'showAlert',
          context: actionContext,
        });
      }
      return false;
    }
    const payload = encodeClientEnvelope(env);
    this.tcWs.send(payload);
    return true;
  }

  private sendToSD(msg: Record<string, unknown>): void {
    if (!this.sdWs || this.sdWs.readyState !== WebSocket.OPEN) {
      return;
    }
    this.sdWs.send(JSON.stringify(msg));
  }

  private handleStreamDeckMessage(msg: SDIncomingMessage): void {
    switch (msg.event) {
      case 'didReceiveGlobalSettings': {
        const s = (msg.payload?.settings ?? {}) as GlobalSettings;
        const prevUrl = this.buildTownCrierUrl();
        this.globalSettings = {
          ...this.globalSettings,
          ...s,
        };
        if (this.buildTownCrierUrl() !== prevUrl) {
          this.connectTownCrier();
        }
        break;
      }

      case 'willAppear': {
        if (msg.context && msg.action) {
          this.actions.set(msg.context, {
            action: msg.action,
            context: msg.context,
            settings: msg.payload?.settings ?? {},
          });
          this.refreshAction(this.actions.get(msg.context)!);
        }
        break;
      }

      case 'willDisappear': {
        if (msg.context) {
          this.actions.delete(msg.context);
        }
        break;
      }

      case 'didReceiveSettings': {
        if (msg.context && this.actions.has(msg.context)) {
          const inst = this.actions.get(msg.context)!;
          inst.settings = msg.payload?.settings ?? {};
          this.refreshAction(inst);
        }
        break;
      }

      case 'keyDown': {
        if (msg.context && msg.action) {
          const inst = this.actions.get(msg.context) ?? {
            action: msg.action,
            context: msg.context,
            settings: msg.payload?.settings ?? {},
          };
          this.handleKeyDown(inst);
        }
        break;
      }

      case 'dialRotate': {
        if (msg.context && msg.action) {
          const inst = this.actions.get(msg.context) ?? {
            action: msg.action,
            context: msg.context,
            settings: msg.payload?.settings ?? {},
          };
          const ticks = typeof msg.payload?.ticks === 'number' ? msg.payload.ticks : 0;
          this.handleDialRotate(inst, ticks);
        }
        break;
      }

      case 'dialPress': {
        if (msg.context && msg.action) {
          const inst = this.actions.get(msg.context) ?? {
            action: msg.action,
            context: msg.context,
            settings: msg.payload?.settings ?? {},
          };
          this.handleKeyDown(inst);
        }
        break;
      }
    }
  }

  private handleKeyDown(inst: ActionInstance): void {
    switch (inst.action) {
      case ACTION_MODE: {
        const targetSetting = String(inst.settings.targetMode ?? 'cycle');
        if (targetSetting === 'always_on') {
          this.sendProto(
            {
              requestId: this.nextReqId(),
              command: { kind: 'setTtsMode', value: { mode: TTSOutputMode.ALWAYS_ON } },
            },
            inst.context,
          );
        } else if (targetSetting === 'auto_afk' || targetSetting === 'auto_idle') {
          this.sendProto(
            {
              requestId: this.nextReqId(),
              command: { kind: 'setTtsMode', value: { mode: TTSOutputMode.AUTO_IDLE } },
            },
            inst.context,
          );
        } else if (targetSetting === 'always_off') {
          this.sendProto(
            {
              requestId: this.nextReqId(),
              command: { kind: 'setTtsMode', value: { mode: TTSOutputMode.ALWAYS_OFF } },
            },
            inst.context,
          );
        } else {
          this.sendProto(
            {
              requestId: this.nextReqId(),
              command: { kind: 'cycleTtsMode' },
            },
            inst.context,
          );
        }
        break;
      }

      case ACTION_PANIC: {
        const opSetting = String(inst.settings.operation ?? 'toggle');
        let action = PanicAction.TOGGLE;
        if (opSetting === 'mute') action = PanicAction.MUTE;
        else if (opSetting === 'unmute' || opSetting === 'clear') action = PanicAction.CLEAR;
        this.sendProto(
          {
            requestId: this.nextReqId(),
            command: { kind: 'panic', value: { action } },
          },
          inst.context,
        );
        break;
      }

      case ACTION_QUEUE: {
        const qSetting = String(inst.settings.queueOperation ?? 'toggle_pause');
        let qAction = this.lastState.paused ? QueueAction.RESUME : QueueAction.PAUSE;
        if (qSetting === 'pause') qAction = QueueAction.PAUSE;
        else if (qSetting === 'resume') qAction = QueueAction.RESUME;
        else if (qSetting === 'skip') qAction = QueueAction.SKIP;
        else if (qSetting === 'clear') qAction = QueueAction.CLEAR;
        this.sendProto(
          {
            requestId: this.nextReqId(),
            command: { kind: 'queueAction', value: { action: qAction } },
          },
          inst.context,
        );
        break;
      }

      case ACTION_SPEAK: {
        const rawText = String(inst.settings.text ?? '').trim();
        const text = rawText.length > 0 ? rawText : 'Hello from Stream Deck!';
        const voiceId = String(inst.settings.voiceId ?? '').trim();
        const speed = Number(inst.settings.speed ?? 1.0);
        this.sendProto(
          {
            requestId: this.nextReqId(),
            command: {
              kind: 'speak',
              value: { text, voiceId, speed: Number.isFinite(speed) && speed > 0 ? speed : 1.0 },
            },
          },
          inst.context,
        );
        break;
      }

      case ACTION_VOLUME: {
        const stepPct = Number(inst.settings.step ?? 5);
        const delta = (Number.isFinite(stepPct) ? stepPct : 5) / 100.0;
        this.sendProto(
          {
            requestId: this.nextReqId(),
            command: {
              kind: 'setVolume',
              value: { delta },
            },
          },
          inst.context,
        );
        break;
      }
    }
  }

  private handleDialRotate(inst: ActionInstance, ticks: number): void {
    if (inst.action === ACTION_VOLUME && ticks !== 0) {
      const stepPct = Number(inst.settings.step ?? 5);
      const delta = (ticks * (Number.isFinite(stepPct) ? stepPct : 5)) / 100.0;
      this.sendProto(
        {
          requestId: this.nextReqId(),
          command: {
            kind: 'setVolume',
            value: { delta },
          },
        },
        inst.context,
      );
    }
  }

  private refreshAllActions(): void {
    for (const inst of this.actions.values()) {
      this.refreshAction(inst);
    }
  }

  private clearNativeTitle(context: string): void {
    this.sendToSD({
      event: 'setTitle',
      context,
      payload: { title: '', target: 0 },
    });
  }

  private refreshAction(inst: ActionInstance): void {
    const s = this.lastState;
    this.clearNativeTitle(inst.context);

    switch (inst.action) {
      case ACTION_MODE: {
        let image = svgDataUri('#0F172A', '#38BDF8', 'mode_on', 'TTS MODE', 'ALWAYS ON', '#38BDF8');
        if (s.outputMode === TTSOutputMode.AUTO_IDLE) {
          const sub = s.speakingActive ? 'AFK: LIVE' : 'AFK: IDLE';
          const accent = s.speakingActive ? '#22C55E' : '#F59E0B';
          image = svgDataUri('#0F172A', accent, 'mode_afk', 'AUTO-AFK', sub, accent);
        } else if (s.outputMode === TTSOutputMode.ALWAYS_OFF) {
          image = svgDataUri('#0F172A', '#64748B', 'mode_off', 'TTS MODE', 'ALWAYS OFF', '#94A3B8');
        }
        this.sendToSD({
          event: 'setImage',
          context: inst.context,
          payload: { image, target: 0 },
        });
        break;
      }

      case ACTION_PANIC: {
        const image = s.panicked
          ? svgDataUri('#450A0A', '#EF4444', 'panic_muted', 'PANIC', 'MUTED', '#EF4444')
          : svgDataUri('#0F172A', '#22C55E', 'panic_safe', 'AUDIO', 'SAFE', '#22C55E');
        this.sendToSD({
          event: 'setImage',
          context: inst.context,
          payload: { image, target: 0 },
        });
        this.sendToSD({
          event: 'setState',
          context: inst.context,
          payload: { state: s.panicked ? 1 : 0 },
        });
        break;
      }

      case ACTION_QUEUE: {
        const qSetting = String(inst.settings.queueOperation ?? 'toggle_pause');
        const status = s.paused ? 'PAUSED' : 'ACTIVE';
        const accent = s.paused ? '#F59E0B' : '#38BDF8';
        const icon: IconKind = s.paused ? 'queue_paused' : 'queue_active';
        let topLabel = `QUEUE: ${s.queueDepth}`;
        let bottomLabel = status;
        if (qSetting === 'skip') {
          topLabel = 'SKIP SPEECH';
          bottomLabel = `QUEUE: ${s.queueDepth}`;
        } else if (qSetting === 'clear') {
          topLabel = 'CLEAR QUEUE';
          bottomLabel = `QUEUE: ${s.queueDepth}`;
        }
        const image = svgDataUri('#0F172A', accent, icon, topLabel, bottomLabel, accent);
        this.sendToSD({
          event: 'setImage',
          context: inst.context,
          payload: { image, target: 0 },
        });
        break;
      }

      case ACTION_SPEAK: {
        const rawLabel = String(inst.settings.label ?? '').trim();
        const label = rawLabel.length > 0 ? rawLabel.slice(0, 12) : 'SPEAK';
        const image = svgDataUri('#0F172A', '#38BDF8', 'speak', 'TOWNCRIER', label, '#38BDF8');
        this.sendToSD({
          event: 'setImage',
          context: inst.context,
          payload: { image, target: 0 },
        });
        break;
      }

      case ACTION_VOLUME: {
        const volPct = Math.round((s.volume ?? 0) * 100);
        const image = svgDataUri('#0F172A', '#38BDF8', 'volume', 'VOLUME', `${volPct}%`, '#38BDF8');
        this.sendToSD({
          event: 'setImage',
          context: inst.context,
          payload: { image, target: 0 },
        });
        this.sendToSD({
          event: 'setFeedback',
          context: inst.context,
          payload: {
            title: 'Master Volume',
            value: `${volPct}%`,
            indicator: { value: volPct },
          },
        });
        break;
      }
    }
  }
}

if (require.main === module) {
  const plugin = new TownCrierStreamDeckPlugin();
  plugin.startFromArgs(process.argv.slice(2));
}
