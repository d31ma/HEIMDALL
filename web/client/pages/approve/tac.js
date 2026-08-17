import { api, session, escape, showError, whenReady } from '/shared/scripts/api.js'

document.title = 'Approve a device — HEIMDALL'

// The moment of this page: a person attaches their identity to a terminal
// they are (hopefully) looking at. The code was read off that terminal's
// screen; nothing here can mint one, and approving proves this browser's
// session to SESAME rather than naming anyone.
export default class ApprovePage {
  static __tachyonOnMount = ['mount']

  async mount() {
    // The runtime awaits onMount before it renders the document, so this
    // must not block on elements the render creates. Kick the flow off;
    // whenReady resolves once the renderer has run.
    void this.start()
  }

  async start() {
    try {
      const [main, identity] = await whenReady(['hd-main', 'hd-identity'])
      this.main = main
      this.identity = identity
      if (!(await session.active())) {
        // Approval requires being signed in: the whole mechanism is this
        // browser proving its session.
        window.location.href = '/'
        return
      }
      this.form('')
    } catch (error) {
      document.body.textContent = error.message
    } finally {
      this.main?.setAttribute('aria-busy', 'false')
    }
  }

  form(message) {
    this.main.innerHTML = `
      <section class="hd-centre flex justify-center pt-12">
        <div class="w-card hd-login p-[1.6rem] w-[min(26rem,100%)] flex flex-col gap-[0.8rem]">
          <h1 class="hd-h1 text-[clamp(1.4rem,3vw,1.9rem)] mt-0 mb-[0.4rem]">Approve a device</h1>
          <p class="hd-muted">
            A terminal running <code>heimdall login --device</code> showed a short code.
            Enter it here only if that terminal is yours and in front of you.
          </p>
          <div id="hd-approve-error" role="alert">${message ? `<div class="w-alert w-alert--color-error">${escape(message)}</div>` : ''}</div>
          <form id="hd-lookup">
            <label class="hd-field flex flex-col gap-1 text-[0.9rem]">
              <span>Device code</span>
              <input id="hd-code" class="py-[0.55rem] px-[0.7rem] rounded-md border border-[var(--w-border,#999)] bg-[var(--w-surface,#fff)] text-base focus-visible:outline-2 focus-visible:outline-[var(--w-primary,#3b5bdb)] focus-visible:outline-offset-2" autocomplete="off" autofocus
                     placeholder="ABCD-EFGH" spellcheck="false">
            </label>
            <button class="w-btn w-btn-filled w-btn-primary" type="submit">Look up</button>
          </form>
        </div>
      </section>`

    document.getElementById('hd-lookup').addEventListener('submit', async (event) => {
      event.preventDefault()
      const code = document.getElementById('hd-code').value.trim()
      if (!code) return
      try {
        const pending = await api.deviceLookup(code)
        this.confirm(code, pending)
      } catch (error) {
        showError(document.getElementById('hd-approve-error'), error)
      }
    })
  }

  confirm(code, pending) {
    this.main.innerHTML = `
      <section class="hd-centre flex justify-center pt-12">
        <div class="w-card hd-login p-[1.6rem] w-[min(26rem,100%)] flex flex-col gap-[0.8rem]">
          <h1 class="hd-h1 text-[clamp(1.4rem,3vw,1.9rem)] mt-0 mb-[0.4rem]">Approve this device?</h1>
          <dl class="hd-facts flex gap-6 max-sm:gap-4 flex-wrap m-0">
            <div><dt>Code</dt><dd><code>${escape(code)}</code></dd></div>
            <div><dt>Client</dt><dd>${escape(pending.client_name || pending.client_id || 'heimdall-cli')}</dd></div>
          </dl>
          <p class="hd-muted">
            Approving signs that terminal in as <strong>you</strong>. If you did not just run
            <code>heimdall login --device</code>, deny it — someone may be phishing this code.
          </p>
          <div id="hd-approve-error" role="alert"></div>
          <div class="hd-actions flex gap-2 flex-wrap">
            <button id="hd-deny" class="w-btn w-btn-outlined" type="button">Deny</button>
            <button id="hd-approve" class="w-btn w-btn-filled w-btn-primary" type="button">Approve</button>
          </div>
        </div>
      </section>`

    document.getElementById('hd-approve').addEventListener('click', async () => {
      try {
        await api.deviceApprove(code)
        this.done('The device is signed in. You can close this page.')
      } catch (error) {
        showError(document.getElementById('hd-approve-error'), error)
      }
    })
    document.getElementById('hd-deny').addEventListener('click', async () => {
      try {
        await api.deviceDeny(code)
        this.done('Denied. The terminal has been told to stop waiting.')
      } catch (error) {
        showError(document.getElementById('hd-approve-error'), error)
      }
    })
  }

  done(message) {
    this.main.innerHTML = `
      <section class="hd-centre flex justify-center pt-12">
        <div class="w-card hd-login p-[1.6rem] w-[min(26rem,100%)] flex flex-col gap-[0.8rem]">
          <h1 class="hd-h1 text-[clamp(1.4rem,3vw,1.9rem)] mt-0 mb-[0.4rem]">Done</h1>
          <p>${escape(message)}</p>
          <a class="hd-link" href="/">Back to applications</a>
        </div>
      </section>`
  }
}
