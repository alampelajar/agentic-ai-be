import { registerForms } from './forms.js'

// Every API route of the site is registered here. A new route file in this folder
// exports a register function, which is imported and called below.
export function registerRoutes(api, db) {
  registerForms(api, db)
}
