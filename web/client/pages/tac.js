import { api, session } from '/shared/scripts/api.js'

document.title = 'HEIMDALL'

// The page companion, Tac's declarative shape: the markup lives in tac.html
// with <if>/<loop> control tags and on:event bindings; this class holds only
// data and behaviour. After each state change it asks the runtime to
// re-render the template against the instance's fields.
export default class HomePage {
  static __tachyonOnMount = ['mount']

  constructor() {
    this.view = 'loading'
    this.busy = true
    this.signedIn = false
    this.signingIn = false
    this.identifier = ''
    this.loginError = ''
    this.pageError = ''
    this.projects = []
    this.term = ''
    this.visibleProjects = []
  }

  async rerender() {
    await globalThis.__tc_rerender?.()
  }

  async mount() {
    // The runtime awaits onMount before the first render, so this kicks the
    // data flow off without blocking: the first paint shows the loading
    // view, and the re-render swaps in whatever the session says.
    void this.refresh()
  }

  async refresh() {
    this.busy = true
    this.pageError = ''
    try {
      if (await session.active()) {
        await this.loadProjects()
        this.signedIn = true
        this.applyFilter()
        this.view = this.projects.length ? 'list' : 'empty'
      } else {
        this.signedIn = false
        this.view = 'login'
      }
    } catch (error) {
      this.pageError = error.message
    } finally {
      this.busy = false
      await this.rerender()
    }
  }

  // loadProjects shapes the answer for the template: hrefs are precomputed
  // here because the bounded expression language deliberately has no calls
  // like encodeURIComponent — building data is the class's job.
  async loadProjects() {
    const { projects } = await api.projects()
    this.projects = await Promise.all(
      projects.map(async (project) => {
        const { applications } = await api.apps(project.name)
        return {
          name: project.name,
          empty: applications.length === 0,
          applications: applications.map((application) => ({
            name: application.name,
            path: application.path || '—',
            ref: application.ref || 'default',
            suspended: !!application.suspended,
            href: `/apps/${encodeURIComponent(project.name)}/${encodeURIComponent(application.name)}`,
          })),
        }
      }),
    )
  }

  async signIn(event) {
    event.preventDefault()
    // Read the fields before any re-render: re-rendering rebuilds the form,
    // and a rebuilt input is empty. Only the username is data-bound back in
    // (:value="identifier") — the password is deliberately not kept.
    this.identifier = document.getElementById('hd-user').value.trim()
    const password = document.getElementById('hd-password').value
    const totp = document.getElementById('hd-totp').value.trim()
    this.signingIn = true
    this.loginError = ''
    await this.rerender()
    try {
      await session.login(this.identifier, password, totp)
      await this.refresh()
    } catch (error) {
      // Every failure mode returns the same message on purpose; repeating
      // it verbatim is what keeps this page from disclosing which part was
      // wrong.
      this.loginError = error.message
      await this.rerender()
    } finally {
      this.signingIn = false
    }
  }

  async signOut() {
    await session.logout()
    this.projects = []
    await this.refresh()
  }

  // Filtering is data like everything else: the runtime re-renders after
  // every event binding, so hiding rows in the DOM would be undone a tick
  // later. The template loops over visibleProjects; a project whose rows
  // all filter away disappears with them, so the list never shows an empty
  // shell. The input's :value binding keeps the term across re-renders.
  applyFilter() {
    const term = this.term.trim().toLowerCase()
    this.visibleProjects = this.projects
      .map((project) => {
        const applications = term === ''
          ? project.applications
          : project.applications.filter((application) =>
              `${application.name} ${application.path} ${application.ref}`.toLowerCase().includes(term))
        return { ...project, applications, empty: project.empty && term === '' }
      })
      .filter((project) => term === '' || project.applications.length > 0)
  }

  async filter(event) {
    this.term = event?.target?.value || ''
    this.applyFilter()
    await this.rerender()
  }
}
