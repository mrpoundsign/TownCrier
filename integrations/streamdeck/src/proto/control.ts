// Code generated for proto/control/v1/control.proto — DO NOT EDIT MANUALLY.
// Strict TypeScript Protocol Buffers wire encoder & decoder for TownCrier Control API v1.

export enum TTSOutputMode {
  UNSPECIFIED = 0,
  ALWAYS_ON = 1,
  AUTO_IDLE = 2,
  ALWAYS_OFF = 3,
}

export enum PanicAction {
  UNSPECIFIED = 0,
  TOGGLE = 1,
  MUTE = 2,
  CLEAR = 3,
}

export enum QueueAction {
  UNSPECIFIED = 0,
  PAUSE = 1,
  RESUME = 2,
  SKIP = 3,
  CLEAR = 4,
}

export interface SetTTSModeCommand {
  mode: TTSOutputMode;
  idleTimeoutSeconds?: number;
}

export interface SpeakCommand {
  text: string;
  voiceId?: string;
  speed?: number;
  priority?: number;
  username?: string;
}

export interface PanicCommand {
  action: PanicAction;
}

export interface QueueActionCommand {
  action: QueueAction;
}

export interface SetVolumeCommand {
  volume?: number;
  delta?: number;
}

export type ClientCommand =
  | { kind: "getState" }
  | { kind: "setTtsMode"; value: SetTTSModeCommand }
  | { kind: "cycleTtsMode" }
  | { kind: "speak"; value: SpeakCommand }
  | { kind: "panic"; value: PanicCommand }
  | { kind: "queueAction"; value: QueueActionCommand }
  | { kind: "setVolume"; value: SetVolumeCommand };

export interface ClientEnvelope {
  requestId?: string;
  command: ClientCommand;
}

export interface StateSnapshot {
  ttsEnabled: boolean;
  outputMode: TTSOutputMode;
  userIdle: boolean;
  idleSeconds: number;
  idleTimeoutSeconds: number;
  speakingActive: boolean;
  paused: boolean;
  panicked: boolean;
  queueDepth: number;
  volume: number;
  currentVoice: string;
  currentlySpeaking: string;
}

export interface SpeechEvent {
  id: string;
  text: string;
  voiceId: string;
  username: string;
  platform: string;
  finished: boolean;
}

export interface ServerEnvelope {
  requestId: string;
  ok: boolean;
  error: string;
  state?: StateSnapshot;
  speechEvent?: SpeechEvent;
}

class ProtoWriter {
  private chunks: number[] = [];

  writeVarint(value: number): void {
    let v = value >>> 0;
    while (v >= 0x80) {
      this.chunks.push((v & 0x7f) | 0x80);
      v >>>= 7;
    }
    this.chunks.push(v);
  }

  writeTag(fieldNumber: number, wireType: number): void {
    this.writeVarint((fieldNumber << 3) | wireType);
  }

  writeBytes(fieldNumber: number, bytes: Uint8Array): void {
    this.writeTag(fieldNumber, 2);
    this.writeVarint(bytes.length);
    for (let i = 0; i < bytes.length; i++) {
      this.chunks.push(bytes[i]!);
    }
  }

  writeString(fieldNumber: number, str: string): void {
    if (!str) return;
    const encoded = new TextEncoder().encode(str);
    this.writeBytes(fieldNumber, encoded);
  }

  writeFloat32(fieldNumber: number, val: number): void {
    this.writeTag(fieldNumber, 5);
    const buf = new ArrayBuffer(4);
    new DataView(buf).setFloat32(0, val, true);
    const bytes = new Uint8Array(buf);
    for (let i = 0; i < 4; i++) {
      this.chunks.push(bytes[i]!);
    }
  }

  finish(): Uint8Array {
    return new Uint8Array(this.chunks);
  }
}

export function encodeClientEnvelope(env: ClientEnvelope): Uint8Array {
  const w = new ProtoWriter();
  if (env.requestId) {
    w.writeString(1, env.requestId);
  }

  const cmd = env.command;
  switch (cmd.kind) {
    case "getState": {
      w.writeBytes(2, new Uint8Array(0));
      break;
    }
    case "setTtsMode": {
      const sub = new ProtoWriter();
      if (cmd.value.mode !== TTSOutputMode.UNSPECIFIED) {
        sub.writeTag(1, 0);
        sub.writeVarint(cmd.value.mode);
      }
      if (cmd.value.idleTimeoutSeconds !== undefined) {
        sub.writeTag(2, 0);
        sub.writeVarint(cmd.value.idleTimeoutSeconds);
      }
      w.writeBytes(3, sub.finish());
      break;
    }
    case "cycleTtsMode": {
      w.writeBytes(4, new Uint8Array(0));
      break;
    }
    case "speak": {
      const sub = new ProtoWriter();
      sub.writeString(1, cmd.value.text);
      if (cmd.value.voiceId) {
        sub.writeString(2, cmd.value.voiceId);
      }
      if (cmd.value.speed !== undefined && cmd.value.speed > 0) {
        sub.writeFloat32(3, cmd.value.speed);
      }
      if (cmd.value.priority !== undefined && cmd.value.priority !== 0) {
        sub.writeTag(4, 0);
        sub.writeVarint(cmd.value.priority);
      }
      if (cmd.value.username) {
        sub.writeString(5, cmd.value.username);
      }
      w.writeBytes(5, sub.finish());
      break;
    }
    case "panic": {
      const sub = new ProtoWriter();
      if (cmd.value.action !== PanicAction.UNSPECIFIED) {
        sub.writeTag(1, 0);
        sub.writeVarint(cmd.value.action);
      }
      w.writeBytes(6, sub.finish());
      break;
    }
    case "queueAction": {
      const sub = new ProtoWriter();
      if (cmd.value.action !== QueueAction.UNSPECIFIED) {
        sub.writeTag(1, 0);
        sub.writeVarint(cmd.value.action);
      }
      w.writeBytes(7, sub.finish());
      break;
    }
    case "setVolume": {
      const sub = new ProtoWriter();
      if (cmd.value.volume !== undefined) {
        sub.writeFloat32(1, cmd.value.volume);
      }
      if (cmd.value.delta !== undefined) {
        sub.writeFloat32(2, cmd.value.delta);
      }
      w.writeBytes(8, sub.finish());
      break;
    }
  }

  return w.finish();
}

class ProtoReader {
  private pos = 0;
  private view: DataView;

  constructor(private buf: Uint8Array) {
    this.view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  }

  eof(): boolean {
    return this.pos >= this.buf.length;
  }

  readVarint(): number {
    let result = 0;
    let shift = 0;
    while (this.pos < this.buf.length) {
      const b = this.buf[this.pos++]!;
      result |= (b & 0x7f) << shift;
      if ((b & 0x80) === 0) {
        return result >>> 0;
      }
      shift += 7;
      if (shift > 35) {
        // Consume any remaining continuation bytes for 64-bit varints
        while (this.pos < this.buf.length && (this.buf[this.pos++]! & 0x80) !== 0) {
          // skip high bits
        }
        return result >>> 0;
      }
    }
    return result >>> 0;
  }

  readBytes(): Uint8Array {
    const len = this.readVarint();
    const start = this.pos;
    this.pos = Math.min(this.buf.length, this.pos + len);
    return this.buf.subarray(start, this.pos);
  }

  readString(): string {
    return new TextDecoder().decode(this.readBytes());
  }

  readFloat32(): number {
    if (this.pos + 4 > this.buf.length) {
      this.pos = this.buf.length;
      return 0;
    }
    const v = this.view.getFloat32(this.pos, true);
    this.pos += 4;
    return v;
  }

  skip(wireType: number): void {
    switch (wireType) {
      case 0:
        this.readVarint();
        break;
      case 1:
        this.pos = Math.min(this.buf.length, this.pos + 8);
        break;
      case 2: {
        const len = this.readVarint();
        this.pos = Math.min(this.buf.length, this.pos + len);
        break;
      }
      case 5:
        this.pos = Math.min(this.buf.length, this.pos + 4);
        break;
      default:
        this.pos = this.buf.length;
        break;
    }
  }
}

function decodeStateSnapshot(bytes: Uint8Array): StateSnapshot {
  const r = new ProtoReader(bytes);
  const s: StateSnapshot = {
    ttsEnabled: false,
    outputMode: TTSOutputMode.UNSPECIFIED,
    userIdle: false,
    idleSeconds: 0,
    idleTimeoutSeconds: 15,
    speakingActive: false,
    paused: false,
    panicked: false,
    queueDepth: 0,
    volume: 1.0,
    currentVoice: "",
    currentlySpeaking: "",
  };

  while (!r.eof()) {
    const tag = r.readVarint();
    const field = tag >>> 3;
    const wire = tag & 7;
    switch (field) {
      case 1:
        s.ttsEnabled = r.readVarint() !== 0;
        break;
      case 2:
        s.outputMode = r.readVarint() as TTSOutputMode;
        break;
      case 3:
        s.userIdle = r.readVarint() !== 0;
        break;
      case 4:
        s.idleSeconds = r.readVarint();
        break;
      case 5:
        s.idleTimeoutSeconds = r.readVarint();
        break;
      case 6:
        s.speakingActive = r.readVarint() !== 0;
        break;
      case 7:
        s.paused = r.readVarint() !== 0;
        break;
      case 8:
        s.panicked = r.readVarint() !== 0;
        break;
      case 9:
        s.queueDepth = r.readVarint();
        break;
      case 10:
        s.volume = r.readFloat32();
        break;
      case 11:
        s.currentVoice = r.readString();
        break;
      case 12:
        s.currentlySpeaking = r.readString();
        break;
      default:
        r.skip(wire);
        break;
    }
  }
  return s;
}

function decodeSpeechEvent(bytes: Uint8Array): SpeechEvent {
  const r = new ProtoReader(bytes);
  const ev: SpeechEvent = {
    id: "",
    text: "",
    voiceId: "",
    username: "",
    platform: "",
    finished: false,
  };
  while (!r.eof()) {
    const tag = r.readVarint();
    const field = tag >>> 3;
    const wire = tag & 7;
    switch (field) {
      case 1:
        ev.id = r.readString();
        break;
      case 2:
        ev.text = r.readString();
        break;
      case 3:
        ev.voiceId = r.readString();
        break;
      case 4:
        ev.username = r.readString();
        break;
      case 5:
        ev.platform = r.readString();
        break;
      case 6:
        ev.finished = r.readVarint() !== 0;
        break;
      default:
        r.skip(wire);
        break;
    }
  }
  return ev;
}

export function decodeServerEnvelope(bytes: Uint8Array): ServerEnvelope {
  const r = new ProtoReader(bytes);
  const env: ServerEnvelope = {
    requestId: "",
    ok: false,
    error: "",
  };
  while (!r.eof()) {
    const tag = r.readVarint();
    const field = tag >>> 3;
    const wire = tag & 7;
    switch (field) {
      case 1:
        env.requestId = r.readString();
        break;
      case 2:
        env.ok = r.readVarint() !== 0;
        break;
      case 3:
        env.error = r.readString();
        break;
      case 4:
        env.state = decodeStateSnapshot(r.readBytes());
        break;
      case 5:
        env.speechEvent = decodeSpeechEvent(r.readBytes());
        break;
      default:
        r.skip(wire);
        break;
    }
  }
  return env;
}
