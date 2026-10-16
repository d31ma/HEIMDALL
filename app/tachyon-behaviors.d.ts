export {};

declare global {
  type BehaviorJsonValue = null | boolean | number | string | BehaviorJsonValue[] | { [key: string]: BehaviorJsonValue };
  type BehaviorJsonObject = { [key: string]: BehaviorJsonValue };
  type BehaviorConstructor = new (...arguments_: never[]) => object;
  type BehaviorDeclaredConstructor = abstract new (...arguments_: never[]) => object;
  /** integer: a whole number. Use it for Settings fields that must not have a fraction, like `size: integer = 20`. */
  type integer = number;

  interface BehaviorExecution {
    readonly environment: 'tac' | 'yon';
    readonly partition: string;
    readonly signal: AbortSignal;
    readonly deadline: number;
  }

  interface BehaviorCreateOptions {
    partition: string;
    signal: AbortSignal;
    timeoutMs?: number;
    profiles?: string[];
    properties?: BehaviorJsonObject;
  }

  interface BehaviorCacheConfigOptions {
    name: string;
    ttlMs: number;
    maxBytes: number;
    maxEntries: number;
  }

  type BehaviorCacheResult = { hit: false } | {
    hit: true;
    value: BehaviorJsonValue;
    expiresAt: number;
  };

  interface BehaviorCacheWriteExecution extends BehaviorExecution {
    readonly name: string;
    readonly ttlMs: number;
    readonly maxBytes: number;
    readonly maxEntries: number;
    readonly expiresAt: number;
  }

  interface CacheAdapter {
    get(key: string, execution: BehaviorExecution): BehaviorCacheResult | Promise<BehaviorCacheResult>;
    set(key: string, value: BehaviorJsonValue, execution: BehaviorCacheWriteExecution): void | Promise<void>;
    delete(key: string, execution: BehaviorExecution): void | Promise<void>;
  }

  interface BehaviorPage {
    page: number;
    size: number;
    offset: number;
    sort: string[];
  }

  interface BehaviorPageInput {
    page?: number;
    size?: number;
    offset?: number;
    sort?: string[];
  }

  interface BehaviorPageableDefaultOptions {
    defaultSize: number;
    maxSize: number;
    sort: string[];
  }

  interface BehaviorMappingField {
    target: string;
    source?: string;
    constant?: string | number | boolean | null;
  }

  interface BehaviorMappingOptions {
    fields: readonly BehaviorMappingField[];
  }

  interface BehaviorRetryableOptions {
    maxRetries: number;
    delayMs?: number;
    multiplier?: number;
    maxDelayMs?: number;
    jitterMs?: number;
  }

  interface BehaviorConcurrencyLimitOptions {
    limit: number;
    policy?: 'block' | 'reject';
  }

  interface BehaviorTransactionalOptions {
    adapter: string;
  }

  interface BehaviorCacheMutationOptions {
    method: string;
  }

  interface BehaviorEntityOptions {
    /** The entity adapter that stores this class. In Tac, "indexeddb" keeps the rows in the browser's IndexedDB with no adapter code; any other name comes from the Setup's `entities`. */
    adapter: 'indexeddb' | (string & {});
  }

  interface BehaviorRepositoryOptions<Record = object> {
    fields: (keyof Record & string)[];
    defaultSize: number;
    maxSize: number;
    setup?: string;
  }
  type BehaviorQueryPredicate =
    | { field: string; operator: 'eq' | 'ne' | 'lt' | 'lte' | 'gt' | 'gte' | 'in'; argument: number }
    | { field: string; operator: 'isNull' | 'isNotNull' };
  interface BehaviorQueryOptions {
    operation: 'find' | 'count' | 'exists';
    where: BehaviorQueryPredicate[];
    orderBy: { field: string; direction: 'asc' | 'desc' }[];
  }
  interface BehaviorQueryPlan {
    readonly kind: 'find' | 'count' | 'exists';
    readonly where: readonly { readonly field: string; readonly operator: string; readonly value?: BehaviorJsonValue }[];
    readonly orderBy: readonly { readonly field: string; readonly direction: 'asc' | 'desc' }[];
    readonly page?: BehaviorPage;
  }
  interface BehaviorConfigurationPropertiesOptions {
    prefix: string;
    names?: Record<string, string>;
  }
  interface BehaviorConditionalOptions {
    name: string;
    havingValue: string;
    matchIfMissing: boolean;
  }
  interface BehaviorValueOptions {
    path: string;
    type?: 'string' | 'boolean' | 'integer' | 'number';
    default?: string | boolean | number;
    min?: number;
    max?: number;
  }
  interface BehaviorSizeOptions { min: number; max: number; }

  type BehaviorHttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS' | 'HEAD';
  interface BehaviorHttpExchangeOptions {
    client: string;
    method: BehaviorHttpMethod;
    path: string;
  }
  type BehaviorGatewayOptions =
    | { class: string; method?: string }
    | { command: [string, ...string[]] };

  interface BehaviorHttpInput {
    query?: Record<string, string | number | boolean>;
    headers?: Record<string, string>;
    body?: BehaviorJsonValue;
  }

  interface BehaviorHttpRequest {
    readonly method: BehaviorHttpMethod;
    readonly path: string;
    readonly query: Record<string, string | number | boolean>;
    readonly headers: Record<string, string>;
    readonly body?: BehaviorJsonValue;
    readonly redirect: 'error';
    readonly credentials: 'omit';
    readonly signal: AbortSignal;
    readonly deadline: number;
    readonly maxResponseBytes: 1048576;
  }

  interface HttpAdapter {
    exchange(request: BehaviorHttpRequest, execution: BehaviorExecution): BehaviorJsonValue | Promise<BehaviorJsonValue>;
  }

  interface BehaviorEntityIdentity {
    readonly entity: string;
    readonly table: string;
    readonly idField: string;
  }

  type BehaviorEntityCommand = BehaviorEntityIdentity & { readonly transaction?: string } & (
    | { readonly operation: 'find'; readonly page: BehaviorPage }
    | { readonly operation: 'save'; readonly value: BehaviorJsonObject }
    | { readonly operation: 'delete'; readonly id: string | number }
    | { readonly operation: 'query'; readonly query: BehaviorQueryPlan }
  );

  /** An AllOrNothing method first sends `begin`, which answers `{ transaction: "<id>" }`, then `commit` or `rollback` with that id. */
  type BehaviorTransactionCommand =
    | { readonly operation: 'transaction'; readonly action: 'begin' }
    | { readonly operation: 'transaction'; readonly action: 'commit' | 'rollback'; readonly transaction: string };

  interface EntityAdapter {
    execute(command: BehaviorEntityCommand | BehaviorTransactionCommand, execution: BehaviorExecution): BehaviorJsonValue | Promise<BehaviorJsonValue>;
  }

  interface BehaviorAuthorizationCall {
    readonly operation: 'cache:read' | 'cache:evict' | 'cache:put';
    readonly cache: string;
    readonly type: string;
    readonly method: string;
    readonly keyArguments: readonly BehaviorJsonValue[];
  }

  interface BehaviorConfiguration {
    caches?: Record<string, CacheAdapter>;
    http?: Record<string, HttpAdapter>;
    entities?: Record<string, EntityAdapter>;
    authorize?(call: BehaviorAuthorizationCall, execution: BehaviorExecution): boolean | Promise<boolean>;
    authorizeEntity?(command: BehaviorEntityIdentity & { readonly operation: 'query'; readonly query: BehaviorQueryPlan }, execution: BehaviorExecution): boolean | Promise<boolean>;
  }

  interface BehaviorConfigurationSource {
    configure(execution: BehaviorExecution): BehaviorConfiguration | Promise<BehaviorConfiguration>;
  }

  interface BehaviorRepository {
    find(page?: BehaviorPageInput): Promise<BehaviorJsonValue>;
    save(value: BehaviorJsonObject): Promise<BehaviorJsonValue>;
    delete(id: string | number): Promise<BehaviorJsonValue>;
  }

  interface BehaviorRestClient {
    get(path: string): BehaviorRestCall;
    post(path: string): BehaviorRestCall;
    put(path: string): BehaviorRestCall;
    patch(path: string): BehaviorRestCall;
    delete(path: string): BehaviorRestCall;
    head(path: string): BehaviorRestCall;
    options(path: string): BehaviorRestCall;
  }

  interface BehaviorRestCall {
    query(values: BehaviorJsonObject): BehaviorRestCall;
    header(name: string, value: string): BehaviorRestCall;
    body(value: BehaviorJsonValue): BehaviorRestCall;
    retrieve(): Promise<BehaviorJsonValue>;
  }

  interface BehaviorContext {
    repository(entity: BehaviorConstructor): BehaviorRepository;
    restClient(name: string): BehaviorRestClient;
    evict(instance: object, method: string, arguments_?: readonly BehaviorJsonValue[]): Promise<void>;
    dispose(): void;
  }

  type BehaviorConstructionFields<Value> = Partial<{
    [Key in keyof Value as Value[Key] extends (...arguments_: never[]) => unknown ? never : Key]: Value[Key]
  }> & { behaviors?: BehaviorContext };

  interface BehaviorFactory {
    create(configuration: BehaviorConfigurationSource, options: BehaviorCreateOptions): Promise<BehaviorContext>;
    construct<Value extends BehaviorConstructor>(type: Value, values: BehaviorConstructionFields<InstanceType<Value>>): InstanceType<Value>;
    properties(type: BehaviorConstructor, values: BehaviorJsonObject): BehaviorJsonObject;
    values(type: BehaviorConstructor, application: BehaviorJsonObject): BehaviorJsonObject;
    validate(type: BehaviorConstructor, value: unknown): BehaviorJsonObject;
  }

  const Behaviors: BehaviorFactory;

  interface BehaviorClassAnnotation {
    <Value extends BehaviorConstructor>(value: Value, context: ClassDecoratorContext<Value>): void;
    <Value extends BehaviorConstructor>(value: Value): void;
    <Value extends BehaviorDeclaredConstructor>(value: Value): void;
  }

  interface BehaviorConfigurationAnnotation {
    <Value extends BehaviorConstructor & BehaviorConfigurationSource>(value: Value, context: ClassDecoratorContext<Value>): void;
    <Value extends BehaviorConstructor & BehaviorConfigurationSource>(value: Value): void;
  }

  interface BehaviorRequiredArgsAnnotation<Fields extends string> {
    <Value extends BehaviorConstructor>(
      value: Value & (Exclude<Fields, keyof InstanceType<Value>> extends never ? unknown : never),
      context: ClassDecoratorContext<Value>,
    ): void;
    <Value extends BehaviorConstructor>(value: Value & (Exclude<Fields, keyof InstanceType<Value>> extends never ? unknown : never)): void;
  }

  interface BehaviorMethodAnnotation {
    <Owner, Arguments extends unknown[], Result>(value: (this: Owner, ...arguments_: Arguments) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Result>): void;
    <Value>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<Value>): void;
  }

  interface BehaviorErrorMethodAnnotation {
    <Owner, ErrorType extends Error, Request extends [] | [unknown], Result>(value: (this: Owner, error: ErrorType, ...request: Request) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, error: ErrorType, ...request: Request) => Result>): void;
    <Owner, ErrorType extends Error, Request extends [] | [unknown], Result>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, error: ErrorType, ...request: Request) => Result>): void;
  }

  interface BehaviorAsyncMethodAnnotation {
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue>(value: (this: Owner, ...arguments_: Arguments) => Promise<Result>, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
  }

  /** A request method: leave its body empty, because Tachyon writes it. An empty body returns nothing to TypeScript, so declare the answer as `Promise<Hello | void>`. */
  interface BehaviorRequestMethodAnnotation {
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue | void>(value: (this: Owner, ...arguments_: Arguments) => Promise<Result>, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue | void>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
  }

  /** A cache annotation: on a service method the answer is JSON; on an HTTPClient request method, whose body Tachyon writes, declare `Promise<Hello | void>`. */
  interface BehaviorCacheMethodAnnotation {
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue | void>(value: (this: Owner, ...arguments_: Arguments) => Promise<Result>, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
    <Owner, Arguments extends unknown[], Result extends BehaviorJsonValue | void>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
  }

  interface BehaviorPromiseMethodAnnotation {
    <Owner, Arguments extends unknown[], Result>(value: (this: Owner, ...arguments_: Arguments) => Promise<Result>, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
    <Owner, Arguments extends unknown[], Result>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, ...arguments_: Arguments) => Promise<Result>>): void;
  }

  interface BehaviorStreamRouteAnnotation {
    <Owner, Arguments extends unknown[], Result extends Iterable<unknown> | AsyncIterable<unknown>>(value: (this: Owner, ...arguments_: Arguments) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, ...arguments_: Arguments) => Result>): void;
    <Owner, Arguments extends unknown[], Result extends Iterable<unknown> | AsyncIterable<unknown>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, ...arguments_: Arguments) => Result>): void;
  }

  interface BehaviorStreamRequestAnnotation {
    <Owner, Message extends object, Result extends void | Promise<void>>(value: (this: Owner, message: Message) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, message: Message) => Result>): void;
    <Owner, Message extends object, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, message: Message) => Result>): void;
  }

  interface BehaviorSocketSessionAnnotation {
    <Owner, Result extends void | Promise<void>>(value: (this: Owner, session: SocketSession) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, session: SocketSession) => Result>): void;
    <Owner, Result extends void | Promise<void>>(value: (this: Owner) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, session: SocketSession) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner) => Result>): void;
  }

  interface BehaviorSocketMessageAnnotation {
    <Owner, Message extends object, Result extends void | Promise<void>>(value: (this: Owner, session: SocketSession, message: Message) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, session: SocketSession, message: Message) => Result>): void;
    <Owner, Message extends object, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, session: SocketSession, message: Message) => Result>): void;
  }

  interface BehaviorSocketErrorAnnotation {
    <Owner, Result extends void | Promise<void>>(value: (this: Owner, session: SocketSession, error: unknown) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, session: SocketSession, error: unknown) => Result>): void;
    <Owner, Result extends void | Promise<void>>(value: (this: Owner, session: SocketSession) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, session: SocketSession) => Result>): void;
    <Owner, Result extends void | Promise<void>>(value: (this: Owner) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, session: SocketSession, error: unknown) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, session: SocketSession) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner) => Result>): void;
  }

  /** One browser that has a @SocketRoute open. Jakarta calls this a Session. */
  interface SocketPeer {
    /** The id of this connection. */
    readonly id: string;
    /** Sends a model value to this browser as JSON text. */
    send(message: object): Promise<void>;
  }

  /** The connection that a @SocketRoute method answers. */
  interface SocketSession extends SocketPeer {
    /** Closes this connection. OnClose runs next. */
    close(): Promise<void>;
    /** Returns every open connection of the same path, this one too, like Jakarta's getOpenSessions(). */
    openSessions(): Promise<SocketPeer[]>;
  }

  /** The type of the send method that @SocketRequest adds to its client: write `declare send: SocketSend<ChatMessage>` in the class. */
  type SocketSend<Message extends object> = (message: Message) => Promise<void>;

  interface BehaviorFieldAnnotation {
    <Owner, Value>(value: undefined, context: ClassFieldDecoratorContext<Owner, Value>): void;
    (target: object, propertyKey: string | symbol, descriptor?: undefined): void;
  }

  class ApplicationEventPublisher {
    /** Sends a model value to every listener whose parameter has the same model type. The event name is the name of the value's declared model type, so declare the value with a model type (`const placed: OrderPlaced = {...}`) or write `publishEvent(value satisfies OrderPlaced)`. Pass `true` as the second argument to keep the value for listeners that ask to replay it. */
    publishEvent<Model extends object>(event: Model, retain?: boolean): void;
  }
  /** Options of OnEvent. The event is the model type of the listener's one parameter, so there is no event name here. */
  interface ApplicationEventListenerOptions {
    /** Delivers the kept value of this model, if one was published with `retain`, when the listener starts. */
    replay?: boolean;
    /** A short expression over `value` (the payload) and `event` (the model name), such as `"value.total >= 10"`. The listener runs only when it is true. */
    condition?: string;
    /** A short name for the listener. */
    id?: string;
    /** Removed (ADR 0055): the model type of the payload parameter is the event. Run `ty migrate fix --write`. */
    event?: never;
    /** Removed (ADR 0055): write one OnEvent method per model. */
    events?: never;
  }
  interface ApplicationLifecycleAnnotation {
    <Owner, Result extends void | Promise<void>>(value: (this: Owner) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner) => Result>): void;
    <Owner, Result extends void | Promise<void>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner) => Result>): void;
  }
  type ApplicationEventReturn = void | object | readonly object[];
  interface ApplicationEventListenerAnnotation {
    <Owner, Payload extends object, Result extends ApplicationEventReturn | Promise<ApplicationEventReturn>>(value: (this: Owner, payload: Payload) => Result, context: ClassMethodDecoratorContext<Owner, (this: Owner, payload: Payload) => Result>): void;
    <Owner, Payload extends object, Result extends ApplicationEventReturn | Promise<ApplicationEventReturn>>(target: object, propertyKey: string | symbol, descriptor: TypedPropertyDescriptor<(this: Owner, payload: Payload) => Result>): void;
  }
  interface TacResourceLoader { precache(urls: string[]): Promise<void>; clearAssetCache(): Promise<void>; }
  interface TacTaskScheduler { render(): Promise<void>; }
  /** Clock: the time of the browser, written once in the page (ADR 0065). */
  interface Clock { now(): number; monotonic(): number; timeZone(): string; }
  /** SecureRandom: random values from `crypto.getRandomValues` (ADR 0065). `bytes` takes 0 to 65,536. */
  interface SecureRandom { bytes(count: number): number[]; uuid(): string; }
  /** Connectivity: whether the browser is online. Listen for `ConnectivityChanged` to hear changes (ADR 0065). */
  interface Connectivity { online(): boolean; }
  /** PageVisibility: whether the page is shown. Listen for `VisibilityChanged` to hear changes (ADR 0065). */
  interface PageVisibility { visible(): boolean; }
  /** Preferences: the color scheme, the motion setting and the language of the browser. Listen for `PreferencesChanged` to hear changes (ADR 0065). */
  interface Preferences { scheme(): 'light' | 'dark'; motion(): 'reduce' | 'no-preference'; language(): string; }
  /** Clipboard: copies and reads text (ADR 0065). Grant "clipboard" in tac.browserApis and call it from a click or key handler. `readText` answers with a `ClipboardTextRead` event; a failure sends `BrowserCallFailed`. */
  interface PreferenceStrings { read(key: string): string | null; write(key: string, value: string): boolean; }
  interface DocumentTitle { set(title: string): void; }
  interface TacClipboard { writeText(text: string): null; readText(): null; }
  /** Share: opens the share sheet of the device (ADR 0065). Grant "share" in tac.browserApis and call it from a click or key handler. Answers with `ShareFinished` or `BrowserCallFailed`. */
  interface TacShare { open(title: string, text: string, url: string): null; }
  /** Notifications: asks for permission and shows notifications (ADR 0065). Grant "notifications" in tac.browserApis. `requestPermission` needs a click or key handler and answers with `NotificationPermissionChanged`. */
  interface TacNotifications { permission(): NotificationPermission; requestPermission(): null; show(title: string, body: string): null; }
  /** Geolocation: the position of the device (ADR 0065). Grant "geolocation" in tac.browserApis. `currentPosition` answers with `PositionFound` or `BrowserCallFailed`. */
  interface TacGeolocation { currentPosition(): null; }
  /** WindowControls: the controls of the native app window (ADR 0087). Grant them in tac.application.window.controls. `granted` answers false in a browser tab; a call to a control that is not granted throws. */
  interface TacFilePickerResult { status: 'available' | 'selected' | 'cancelled' | 'read' | 'released' | 'denied' | 'unsupported' | 'unavailable' | 'invalid' | 'disposed' | 'tooLarge'; handle?: string; name?: string; text?: string; }
  interface TacFilePicker { available(): boolean; open(options?: { multiple?: false; types?: ('text/plain' | 'application/json')[] }): Promise<TacFilePickerResult>; readText(handle: string): Promise<TacFilePickerResult>; release(handle: string): Promise<TacFilePickerResult>; }
  interface TacWindowControls { granted(control: 'close' | 'fullscreen' | 'maximize' | 'minimize' | 'resize' | 'title'): boolean; minimize(): null; maximize(): null; fullscreen(): null; close(): null; resize(width: number, height: number): null; setTitle(text: string): null; }
  /** ConnectivityChanged: a built-in event model. The page publishes it when the browser goes online or offline (ADR 0065). */
  interface ConnectivityChanged { online: boolean; }
  /** VisibilityChanged: a built-in event model. The page publishes it when the page is shown or hidden (ADR 0065). */
  interface VisibilityChanged { visible: boolean; }
  /** PreferencesChanged: a built-in event model. The page publishes it when the color scheme, the motion setting or the language changes (ADR 0065). */
  interface PreferencesChanged { scheme: 'light' | 'dark'; motion: 'reduce' | 'no-preference'; language: string; }
  /** ClipboardTextRead: a built-in event model with the text that `Clipboard.readText` read (ADR 0065). */
  interface ClipboardTextRead { text: string; }
  /** ShareFinished: a built-in event model. The share sheet closed after sharing (ADR 0065). */
  interface ShareFinished {}
  /** NotificationPermissionChanged: a built-in event model with the answer to `Notifications.requestPermission` (ADR 0065). */
  interface NotificationPermissionChanged { permission: NotificationPermission; }
  /** PositionFound: a built-in event model with the position that `Geolocation.currentPosition` found (ADR 0065). */
  interface PositionFound { latitude: number; longitude: number; accuracy: number; timestamp: number; }
  /** BrowserCallFailed: a built-in event model. A browser API call failed after it started (ADR 0065). */
  interface BrowserCallFailed { api: string; call: string; message: string; }
  interface TacResourceMap {
    view: HTMLElement;
    lifecycle: AbortSignal;
    resourceLoader: TacResourceLoader;
    taskScheduler: TacTaskScheduler;
    applicationEventPublisher: ApplicationEventPublisher;
    clock: Clock;
    secureRandom: SecureRandom;
    connectivity: Connectivity;
    pageVisibility: PageVisibility;
    preferences: Preferences;
    preferenceStrings: PreferenceStrings;
    documentTitle: DocumentTitle;
    clipboard: TacClipboard;
    share: TacShare;
    notifications: TacNotifications;
    geolocation: TacGeolocation;
    windowControls: TacWindowControls;
    filePicker: TacFilePicker;
  }
  /** OnEvent: this method listens for one event model, the type of its one parameter (`placed(order: OrderPlaced)`). A payload that does not fit the model is not delivered. When the method's return type is a model, the value it returns is published as that model. Write `@OnEvent`, or `@OnEvent({ condition, id, replay })`. */
  const OnEvent: ApplicationEventListenerAnnotation & ((options?: ApplicationEventListenerOptions) => ApplicationEventListenerAnnotation);
  function Order(value: number): ApplicationEventListenerAnnotation;
  const InBackground: BehaviorBareMarker<ApplicationEventListenerAnnotation>;
  type BehaviorBareMarker<Annotation> = Annotation & (() => Annotation);
  const OnStart: BehaviorBareMarker<ApplicationLifecycleAnnotation>;
  const OnStop: BehaviorBareMarker<ApplicationLifecycleAnnotation>;

  const Setup: BehaviorBareMarker<BehaviorConfigurationAnnotation>;
  interface BehaviorTestOptions {
    partition?: string;
  }
  type BehaviorTestPropertyValue = null | boolean | number | string;
  type BehaviorTestProperties = Record<`tac.${string}` | `yon.${string}`, BehaviorTestPropertyValue>;
  /** Test: this class is a test unit. Pass the Setup class it uses, like `@Test(CatalogTestSetup)`, and optionally `{ partition: "tests" }`. */
  function Test(configuration: BehaviorConstructor, options?: BehaviorTestOptions): BehaviorClassAnnotation;
  /** The members that `ty test` gives every @Test class; `ty types` adds them to each test class it finds. */
  interface BehaviorTestUnit {
    /** The context of the running test case. */
    readonly behaviors: BehaviorContext;
    /** Builds the class and fills its needed fields with the test's @Fake fields, like `this.construct(CatalogService)`. */
    construct<Value extends BehaviorConstructor>(type: Value): InstanceType<Value>;
  }
  const TestSetup: BehaviorBareMarker<BehaviorConfigurationAnnotation>;
  function UseProfiles(...profiles: [string, ...string[]]): BehaviorClassAnnotation;
  function TestSettings(properties: BehaviorTestProperties): BehaviorClassAnnotation;
  const FreshStart: BehaviorBareMarker<BehaviorClassAnnotation>;
  const Fake: BehaviorBareMarker<BehaviorFieldAnnotation>;
  type BehaviorTestInput = null | boolean | number | string | BehaviorTestInput[] | { [key: string]: BehaviorTestInput };
  /** ValueSource: Tachyon runs this test case once for each input, like `@ValueSource([1, 2, 3])`, and passes the input as the one argument. */
  function ValueSource(inputs: [BehaviorTestInput, ...BehaviorTestInput[]]): BehaviorMethodAnnotation;
  /** Assert: checks inside a test case, like JUnit's Assertions. A failed check fails the case with its message. */
  class Assert {
    static equals(expected: unknown, actual: unknown, message?: string): void;
    static isTrue(condition: boolean, message?: string): void;
    static throws(action: () => unknown, message?: string): unknown;
  }
  interface BehaviorParameterAnnotation {
    (target: object, propertyKey: string | symbol | undefined, parameterIndex: number): void;
  }
  type BehaviorPathAnnotation<Annotation> = Annotation & ((path?: string) => Annotation);
  type BehaviorNamedParameterAnnotation = BehaviorParameterAnnotation & ((name?: string) => BehaviorParameterAnnotation);
  interface BehaviorHttpClientOptions {
    path?: string;
    adapter?: string;
    setup?: string;
  }
  /** RequestRoute: this class is a controller, and all its routes start with this path, like "/orders". */
  const RequestRoute: BehaviorPathAnnotation<BehaviorClassAnnotation>;
  type BehaviorAccessAnnotation = BehaviorClassAnnotation & BehaviorMethodAnnotation;
  /** Public: anyone may call this route, also a caller who is not signed in. Write it on the controller class or on a route method; the method wins. */
  const Public: BehaviorBareMarker<BehaviorAccessAnnotation>;
  /** SignedIn: any signed-in user may call this route. The @IdentityProvider class says who is signed in. */
  const SignedIn: BehaviorBareMarker<BehaviorAccessAnnotation>;
  /** Allow: only a signed-in user with one of these roles may call this route, like `@Allow(Admin, Editor)`. Each role is a class in server/roles/. */
  function Allow(...roles: [BehaviorConstructor, ...BehaviorConstructor[]]): BehaviorAccessAnnotation;
  /** IdentityProvider: the one class that says who is signed in. Its method `identify(request)` returns `{ user, roles }`, or `null` when nobody is signed in. Put it in server/controllers/<Name>IdentityProvider. */
  const IdentityProvider: BehaviorBareMarker<BehaviorClassAnnotation>;
  /** ErrorHandler: this class handles errors from same-language Yon controllers. Put it in server/controllers/<Name>ErrorHandler. */
  const ErrorHandler: BehaviorBareMarker<BehaviorClassAnnotation>;
  /** HandlesError: handle the native error type declared on the first parameter, with an optional request parameter. This marker takes no arguments. */
  const HandlesError: BehaviorBareMarker<BehaviorErrorMethodAnnotation>;
  /** GetRoute: this method answers GET requests at this path, like "/{id}" (added to the class path). */
  const GetRoute: BehaviorPathAnnotation<BehaviorMethodAnnotation>;
  /** PostRoute: this method answers POST requests at this path (added to the class path). */
  const PostRoute: BehaviorPathAnnotation<BehaviorMethodAnnotation>;
  /** PutRoute: this method answers PUT requests at this path (added to the class path). */
  const PutRoute: BehaviorPathAnnotation<BehaviorMethodAnnotation>;
  /** PatchRoute: this method answers PATCH requests at this path (added to the class path). */
  const PatchRoute: BehaviorPathAnnotation<BehaviorMethodAnnotation>;
  /** DeleteRoute: this method answers DELETE requests at this path (added to the class path). */
  const DeleteRoute: BehaviorPathAnnotation<BehaviorMethodAnnotation>;
  /** StreamRoute: this method answers GET requests at this path with a stream of server-sent events. It yields each message, and its name starts with `stream`, like `async *streamTicks()`. It replaces GetRoute with Stream. */
  const StreamRoute: BehaviorPathAnnotation<BehaviorStreamRouteAnnotation>;
  /** SocketRoute: this class is a controller that keeps a WebSocket open at this path, like "/chat". Tachyon makes one object for each connection, as Jakarta's @ServerEndpoint does. Messages are JSON text, at most 64 KiB, from pages on the same origin. */
  const SocketRoute: BehaviorPathAnnotation<BehaviorClassAnnotation>;
  /** OnOpen: runs this method when a browser opens the socket, like `onOpen(session: SocketSession)`. */
  const OnOpen: BehaviorBareMarker<BehaviorSocketSessionAnnotation>;
  /** OnMessage: runs this method for each message from the browser. The message is checked against the model type of the second parameter first, like `onMessage(session: SocketSession, message: ChatMessage)`. */
  const OnMessage: BehaviorBareMarker<BehaviorSocketMessageAnnotation>;
  /** OnClose: runs this method when the socket closes, always last, like `onClose(session: SocketSession)`. */
  const OnClose: BehaviorBareMarker<BehaviorSocketSessionAnnotation>;
  /** OnError: runs this method when OnOpen or OnMessage fails, like `onError(session: SocketSession, error: unknown)`. The socket then closes with code 1011. */
  const OnError: BehaviorBareMarker<BehaviorSocketErrorAnnotation>;
  /** ResponseStatus: use 200..299 on a route, or 400..599 on HandlesError (which defaults to 500). */
  function ResponseStatus(status: number): BehaviorMethodAnnotation;
  /** FromPath: this argument is a value from the path, like {id}. */
  const FromPath: BehaviorNamedParameterAnnotation;
  /** FromQuery: this argument is a value from the query string (after `?`). */
  const FromQuery: BehaviorNamedParameterAnnotation;
  /** FromBody: this argument is the request body, read as JSON. */
  const FromBody: BehaviorParameterAnnotation & (() => BehaviorParameterAnnotation);
  /** FromHeader: this argument is the value of a request header. */
  const FromHeader: BehaviorNamedParameterAnnotation;
  /** FromCookie: this argument is the value of a cookie. */
  const FromCookie: BehaviorNamedParameterAnnotation;
  /** HTTPClient: this class sends HTTP requests, and their paths start with this path, like "/api/orders". */
  const HTTPClient: BehaviorClassAnnotation & ((options?: string | BehaviorHttpClientOptions) => BehaviorClassAnnotation);
  /** GetRequest: calling this method sends a GET request to this path (added to the class path). */
  const GetRequest: BehaviorPathAnnotation<BehaviorRequestMethodAnnotation>;
  /** PostRequest: calling this method sends a POST request to this path (added to the class path). */
  const PostRequest: BehaviorPathAnnotation<BehaviorRequestMethodAnnotation>;
  /** PutRequest: calling this method sends a PUT request to this path (added to the class path). */
  const PutRequest: BehaviorPathAnnotation<BehaviorRequestMethodAnnotation>;
  /** PatchRequest: calling this method sends a PATCH request to this path (added to the class path). */
  const PatchRequest: BehaviorPathAnnotation<BehaviorRequestMethodAnnotation>;
  /** DeleteRequest: calling this method sends a DELETE request to this path (added to the class path). */
  const DeleteRequest: BehaviorPathAnnotation<BehaviorRequestMethodAnnotation>;
  /** StreamRequest: Tachyon opens a stream of server-sent events at this path (added to the class path) when the client is first used, and closes it with the component or service that uses it. Each message is checked against the model type of the method's one parameter, passed to the method, and then published as that model's event for OnEvent listeners, like `streamTicks(tick: Tick) {}`. */
  const StreamRequest: BehaviorPathAnnotation<BehaviorStreamRequestAnnotation>;
  /** SocketRequest: Tachyon opens a WebSocket at this path (added to the class path) when the client is first used, and closes it with the component or service that uses it. Each message is checked against the model type of the method's one parameter, passed to the method, and published as that model's event for OnEvent listeners, like `receive(message: ChatMessage) {}`. The client also gets `send(message)`: declare it with `declare send: SocketSend<ChatMessage>`. */
  const SocketRequest: BehaviorPathAnnotation<BehaviorStreamRequestAnnotation>;
  /** PathVariable: this argument fills a part of the request path, like {id}. */
  const PathVariable: BehaviorNamedParameterAnnotation;
  /** RequestParam: this argument is sent as a value in the query string (after `?`). */
  const RequestParam: BehaviorNamedParameterAnnotation;
  /** RequestBody: this argument is sent as the request body, as JSON. */
  const RequestBody: BehaviorParameterAnnotation & (() => BehaviorParameterAnnotation);
  /** RequestHeader: this argument is sent as the value of a request header. */
  const RequestHeader: BehaviorNamedParameterAnnotation;
  const Service: BehaviorBareMarker<BehaviorClassAnnotation>;
  /** Helper: marks a class that services use for part of their work. Tachyon creates it and gives it to the classes that ask for it with Needs. */
  const Helper: BehaviorBareMarker<BehaviorClassAnnotation>;
  interface BehaviorScopeOptions { value: 'singleton' | 'prototype'; }
  function Lifetime(options: BehaviorScopeOptions): BehaviorClassAnnotation;
  function Lifetime(lifetime: 'singleton' | 'request' | 'transient'): BehaviorClassAnnotation;
  /** NeedsAll: build this class and fill every declared field from the properties or providers with the same names. It takes no field list, and the class declares no constructor. */
  const NeedsAll: BehaviorBareMarker<BehaviorClassAnnotation>;
  interface BehaviorScheduledOptions { fixedDelaySeconds: number; initialDelaySeconds?: number; }
  function Schedule(options: BehaviorScheduledOptions): BehaviorClassAnnotation;
  const Id: BehaviorBareMarker<BehaviorFieldAnnotation>;
  const Needs: BehaviorBareMarker<BehaviorFieldAnnotation> & ((...providers: [BehaviorConstructor, ...BehaviorConstructor[]]) => BehaviorClassAnnotation);
  function Named(provider: string): BehaviorFieldAnnotation;
  /** LocalState: keep this Tac service field in the browser's localStorage. The value comes back after a reload and in later visits on this device. Values are JSON, at most 64 KiB. Never keep a secret here. */
  const LocalState: BehaviorBareMarker<BehaviorFieldAnnotation>;
  /** SessionState: keep this Tac service field in the browser's sessionStorage. The value comes back after a reload in the same tab only. Values are JSON, at most 64 KiB. Never keep a secret here. */
  const SessionState: BehaviorBareMarker<BehaviorFieldAnnotation>;
  /** CacheSettings: names the cache that this class's Cached methods use, and sets its limits. In Tac, a cache that the Setup does not configure is kept in the browser Cache API (in memory only where that API is missing). */
  function CacheSettings(options: BehaviorCacheConfigOptions): BehaviorClassAnnotation;
  /** Cached: keep this method's answer in the class cache, on a service or an HTTPClient method (ADR 0087). The cache key is the parameters marked @CacheKey, or every parameter when none is marked. */
  const Cached: BehaviorBareMarker<BehaviorCacheMethodAnnotation>;
  /** CacheKey: this parameter is part of the cache key of its @Cached method. */
  const CacheKey: BehaviorParameterAnnotation & (() => BehaviorParameterAnnotation);
  /** ClearCache: after this method runs, remove the cached answer of `method`. Parameters with the same names as that method's key parameters choose the entry. */
  function ClearCache(options: BehaviorCacheMutationOptions): BehaviorCacheMethodAnnotation;
  /** UpdateCache: after this method runs, store its answer as the cached answer of `method`. Parameters are matched by name. */
  function UpdateCache(options: BehaviorCacheMutationOptions): BehaviorCacheMethodAnnotation;
  /** PageDefaults: this parameter is a page request. With sizes, it fills in missing page values; without sizes, it marks the page parameter of a Store query. */
  const PageDefaults: BehaviorParameterAnnotation & ((options?: BehaviorPageableDefaultOptions) => BehaviorParameterAnnotation);
  /** Entity: this class is stored by the named entity adapter. Mark its identifier field with @Id. */
  function Entity(options: BehaviorEntityOptions): BehaviorClassAnnotation;
  function Table(name: string): BehaviorClassAnnotation;
  /** Gateway: Tachyon writes this method: it calls the named class or command. Leave its body empty and declare the answer as `Promise<Report | void>`. */
  function Gateway(options: BehaviorGatewayOptions): BehaviorRequestMethodAnnotation;
  function Description(text: string): BehaviorClassAnnotation;
  function Retry(options: BehaviorRetryableOptions): BehaviorAsyncMethodAnnotation;
  function MaxAtOnce(options: BehaviorConcurrencyLimitOptions): BehaviorAsyncMethodAnnotation;
  function AllOrNothing(options: BehaviorTransactionalOptions): BehaviorAsyncMethodAnnotation;
  const Converter: BehaviorBareMarker<BehaviorClassAnnotation>;
  /** Convert: the compiler writes this method. It reads its one parameter and builds the result from `fields`. */
  function Convert(options: BehaviorMappingOptions): BehaviorPromiseMethodAnnotation;
  /** Store: a contract with no implementation, like a Spring Data repository interface. It names the Entity class it keeps, like `@Store(Product, { fields: ["id"], defaultSize: 20, maxSize: 100 })`, and Tachyon writes each query. `setup` names the Setup that builds its behavior context. */
  function Store<Entity extends BehaviorConstructor>(entity: Entity, options: BehaviorRepositoryOptions<InstanceType<Entity>>): BehaviorClassAnnotation;
  /** Repository: the base of a Store, like Spring Data's CrudRepository. A Store that extends `Repository<Entity, Id>` gets findById, findAll, save and deleteById without declaring them, and Tachyon writes each one. */
  abstract class Repository<Entity extends object, Id extends string | number> {
    /** findById: the entity with this id, or null. */
    findById(id: Id): Promise<Entity | null>;
    /** findAll: one page of entities, ordered by id. Pass `{}` for the first page of the default size. */
    findAll(page: BehaviorPageInput): Promise<Entity[]>;
    /** save: insert or replace the entity, and answer the saved entity. */
    save(entity: Entity): Promise<Entity>;
    /** deleteById: delete the entity with this id, and answer how many entities were deleted. */
    deleteById(id: Id): Promise<number>;
  }
  /** Query: the compiler writes this Store method from the query plan. Mark its page parameter with @PageDefaults. */
  function Query(options: BehaviorQueryOptions): BehaviorPromiseMethodAnnotation;
  /** Settings: fill this class's fields from environment variables that start with `prefix`. Field types and defaults come from the field declarations; put @Min and @Max on number fields. */
  function Settings(options: BehaviorConfigurationPropertiesOptions): BehaviorClassAnnotation;
  function WhenProfile(...profiles: [string, ...string[]]): BehaviorConfigurationAnnotation;
  function WhenSetting(options: BehaviorConditionalOptions): BehaviorConfigurationAnnotation;
  function Setting(options: string | BehaviorValueOptions): BehaviorFieldAnnotation;
  const Checked: BehaviorBareMarker<BehaviorClassAnnotation>;
  /** Check: check this parameter against the fields of its declared type, which is a @Checked class. */
  const Check: BehaviorParameterAnnotation & (() => BehaviorParameterAnnotation);
  const Required: BehaviorBareMarker<BehaviorFieldAnnotation>;
  const NotEmpty: BehaviorBareMarker<BehaviorFieldAnnotation>;
  function Length(options: BehaviorSizeOptions): BehaviorFieldAnnotation;
  function Min(value: number): BehaviorFieldAnnotation;
  function Max(value: number): BehaviorFieldAnnotation;
}
