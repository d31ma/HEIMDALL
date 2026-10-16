/// <reference path="./tachyon-behaviors.d.ts" />
declare module '/shared/modules/*' {
  const value: unknown;
  export default value;
}

declare module '*.css' {
  const value: string;
  export default value;
}

declare global {
  class DataSource {
    query(sql: string, parameters?: readonly (string | number | boolean | null)[]): Promise<Record<string, unknown>[]>;
    execute(sql: string, parameters?: readonly (string | number | boolean | null)[]): Promise<{ affectedRows: number }>;
    command(command: Readonly<Record<string, unknown>>): Promise<unknown>;
  }
  class RedisTemplate {
    get(key: string): Promise<unknown>;
    set(key: string, value: unknown, ttlSeconds: number): Promise<boolean>;
    delete(key: string): Promise<boolean>;
  }
  namespace NodeJS {
    interface ProcessEnv {
      [name: string]: string | undefined;
    }
    interface Process {
      env: ProcessEnv;
    }
  }
  /** Yon only: the handler process. A handler reads its private `YON_APPLICATION_JSON` snapshot from `process.env`. */
  var process: NodeJS.Process;
  /** Yon only: send the request to another program, like `relay(['ruby', 'server/helpers/ReportDelegate.rb'], request)`, and answer with its response. */
  function relay(command: readonly [string, ...string[]], request: Request): Promise<Response>;
  interface HTMLElement {
    tachyonComponent?: {
      instance?: object;
      refresh?: () => void | Promise<void>;
      controller?: AbortController;
    };
  }

  type TacProps = Record<string, unknown>;
  interface TacPlatformContext {
    platform: 'web' | 'native';
    environment: 'browser' | 'server' | 'native';
    os: 'android' | 'ios' | 'linux' | 'macos' | 'web' | 'windows' | 'unknown';
    target: string;
  }

  /** @deprecated Historical declaration only; the current runtime does not install this global. Put platform-specific behavior in a target-native companion of a service, such as `client/services/<Name>Service.swift` or `.kt`, beside its JS or TS file (ADR 0030). */
  const platform: TacPlatformContext['platform'];
  /** @deprecated Historical declaration only; the current runtime does not install this global. Put platform-specific behavior in a target-native companion of a service, such as `client/services/<Name>Service.swift` or `.kt`, beside its JS or TS file (ADR 0030). */
  const environment: TacPlatformContext['environment'];
  /** @deprecated Historical declaration only; the current runtime does not install this global. Put platform-specific behavior in a target-native companion of a service, such as `client/services/<Name>Service.swift` or `.kt`, beside its JS or TS file (ADR 0030). */
  const os: TacPlatformContext['os'];
  /** @deprecated Historical declaration only; the current runtime does not install this global. Put platform-specific behavior in a target-native companion of a service, such as `client/services/<Name>Service.swift` or `.kt`, beside its JS or TS file (ADR 0030). */
  const target: TacPlatformContext['target'];

  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const app: {
    isAvailable(): boolean;
    info(): Promise<Record<string, unknown>>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const clipboard: {
    readText(): Promise<string>;
    writeText(text: string): Promise<unknown>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const fileSystem: {
    readText(path: string): Promise<{ path: string; text: string }>;
    writeText(path: string, text: string): Promise<{ path: string; bytes: number; written: boolean }>;
    readDir(path: string): Promise<{ path: string; entries: Array<{ name: string; type: 'file' | 'directory' }> }>;
    stat(path: string): Promise<{ path: string; exists: boolean; type?: 'file' | 'directory'; size?: number }>;
    mkdir(path: string): Promise<{ path: string; created: boolean }>;
    remove(path: string): Promise<{ path: string; removed: boolean }>;
    paths(): Promise<{ appData: string; cache: string; documents?: string }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const shell: {
    exec(command: string, args?: string[], cwd?: string): Promise<{ command: string; args: string[]; exitCode: number; stdout: string; stderr: string }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const browser: {
    open(url: string): Promise<{ opened: boolean }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const share: {
    text(text: string, title?: string): Promise<{ shared: boolean }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const haptics: {
    impact(): Promise<{ impacted: boolean }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const filePicker: {
    openText(): Promise<{ name: string; text: string }>;
    saveText(name: string, text: string): Promise<{ name: string; saved: boolean }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const secrets: {
    get(key: string): Promise<string | null>;
    set(key: string, value: string): Promise<void>;
    delete(key: string): Promise<void>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const auth: {
    verifyUser(reason: string): Promise<{ verified: boolean; method: 'biometric' | 'device-credential' }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const geolocation: {
    current(options?: PositionOptions): Promise<{
      latitude: number;
      longitude: number;
      accuracy: number;
      altitude: number | null;
      altitudeAccuracy: number | null;
      heading: number | null;
      speed: number | null;
      timestamp: number;
    }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const notifications: {
    show(title: string, options?: NotificationOptions): Promise<{ shown: boolean }>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const media: {
    getUserMedia(constraints: MediaStreamConstraints): Promise<MediaStream>;
  };

  interface TachyonShortcut {
    id: string;
    accelerator: string;
  }
  interface TachyonShortcutListResult {
    shortcuts: TachyonShortcut[];
  }
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (ADR 0030), and deliver its events with the service's `@Needs` `applicationEventPublisher` to an `@OnEvent` method (ADR 0038). */
  const host: {
    invoke<T = unknown>(operation: string, payload?: unknown): Promise<T>;
    on<T = unknown>(event: string, handler: (payload: T) => void): () => void;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (ADR 0030), and deliver its events with the service's `@Needs` `applicationEventPublisher` to an `@OnEvent` method (ADR 0038). */
  const shortcuts: {
    register(options: { id: string; accelerator: string; replace?: boolean }): Promise<TachyonShortcutListResult & { shortcut: TachyonShortcut }>;
    unregister(id: string): Promise<TachyonShortcutListResult & { unregistered: boolean }>;
    unregisterAll(): Promise<TachyonShortcutListResult & { unregistered: number }>;
    list(): Promise<TachyonShortcutListResult>;
  };

  interface TachyonAppWindowState {
    mode?: 'normal' | 'minimized' | 'maximized' | 'fullscreen';
    alwaysOnTop: boolean;
    opacity: number;
    clickThrough?: boolean;
    captureProtection?: boolean;
  }
  /** @deprecated Historical declaration only; the current runtime does not install this global. Use the injected `WindowControls` browser API (`@Needs windowControls`) for the window controls the application declares, or a target-native companion of a service for anything else (ADR 0030). */
  const appWindow: {
    state(): Promise<TachyonAppWindowState>;
    setAlwaysOnTop(enabled: boolean): Promise<TachyonAppWindowState | { updated: true }>;
    setOpacity(value: number): Promise<TachyonAppWindowState | { updated: true }>;
    setClickThrough(enabled: boolean): Promise<TachyonAppWindowState>;
    setCaptureProtection(enabled: boolean): Promise<TachyonAppWindowState>;
  };

  interface TachyonContentSurfaceState {
    id: string;
    open: boolean;
    presentation?: 'composed' | 'detached';
    persistentSession?: boolean;
    url?: string;
    loading?: boolean;
    canGoBack?: boolean;
    canGoForward?: boolean;
  }
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (ADR 0030), and deliver its events with the service's `@Needs` `applicationEventPublisher` to an `@OnEvent` method (ADR 0038). */
  const contentSurface: {
    open(options: { id: string; url: string; persistentSession?: boolean }): Promise<TachyonContentSurfaceState>;
    navigate(id: string, url: string): Promise<TachyonContentSurfaceState>;
    state(id: string): Promise<TachyonContentSurfaceState>;
    goBack(id: string): Promise<TachyonContentSurfaceState>;
    goForward(id: string): Promise<TachyonContentSurfaceState>;
    reload(id: string): Promise<TachyonContentSurfaceState>;
    close(id: string): Promise<TachyonContentSurfaceState & { open: false }>;
  };

  interface TachyonCaptureWindow {
    windowId: string;
    title: string;
    application: string;
    frame: { x: number; y: number; width: number; height: number };
  }
  interface TachyonCaptureResult {
    windowId: string;
    destination: 'clipboard' | 'file' | 'both';
    format: 'png';
    bytes: number;
    clipboard: boolean;
    path: string;
  }
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const screenCapture: {
    state(): Promise<{ supported: boolean; permission: 'granted' | 'prompt' | 'denied' | 'unsupported'; format: 'png'; destinations: Array<'clipboard' | 'file' | 'both'> }>;
    listWindows(options?: { visibleOnly?: boolean; excludeCurrentApp?: boolean }): Promise<{ windows: TachyonCaptureWindow[]; permission: 'granted' | 'prompt' | 'unsupported' }>;
    captureWindow(options: { windowId: string; destination: 'clipboard' | 'file' | 'both'; format?: 'png' }): Promise<TachyonCaptureResult>;
  };
  /** @deprecated Historical declaration only; the current runtime does not install this global. Call the platform SDK from a target-native companion of a service (`client/services/<Name>Service.swift`, `.kt`, `.cs` or `.rs`; ADR 0030), and reach the page through the service's `@Needs` resources such as `view`, `lifecycle`, `taskScheduler` and `applicationEventPublisher` (ADR 0038). */
  const capabilities: {
    supports(capability: string): boolean;
    state(capability: string): Promise<'granted' | 'denied' | 'prompt' | 'unsupported'>;
  };

  interface Window {
    __tachyonNativeHostCall?: (capability: string, payload: string) => string;
    __tachyonNativeUI?: {
      render(): Promise<unknown>;
      dispatch(event: unknown): Promise<unknown>;
      emit(event: unknown): Promise<unknown>;
    };
  }
}

export {};
