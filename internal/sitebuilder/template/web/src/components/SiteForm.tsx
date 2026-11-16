import { useState, type FormEvent } from 'react'
import { site } from '../site'

export type SiteFormField = {
  name: string
  label: string
  type?: 'text' | 'email' | 'tel' | 'textarea' | 'select' | 'date' | 'number'
  required?: boolean
  placeholder?: string
  options?: string[]
}

type SiteFormProps = {
  /** Where the answers are stored, e.g. "contact" or "booking": lower-case letters, digits, hyphens. */
  name: string
  fields: SiteFormField[]
  submitLabel?: string
  successMessage?: string
  className?: string
}

const words = {
  id: { send: 'Kirim', sending: 'Mengirim...', sent: 'Terima kasih, pesan Anda sudah kami terima.', failed: 'Pesan belum terkirim. Coba lagi sebentar lagi.', required: 'wajib diisi' },
  en: { send: 'Send', sending: 'Sending...', sent: 'Thank you, we have received your message.', failed: 'Your message was not sent. Please try again shortly.', required: 'required' },
}

/**
 * A form whose answers the site's server stores. Styled only with the theme tokens, so it
 * fits every design direction. Every field has a visible label.
 */
export function SiteForm({ name, fields, submitLabel, successMessage, className }: SiteFormProps) {
  const text = site.language === 'id' ? words.id : words.en
  const [state, setState] = useState<'idle' | 'sending' | 'sent' | 'failed'>('idle')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = Object.fromEntries(new FormData(form).entries())
    setState('sending')
    try {
      const response = await fetch(`/api/forms/${name}`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(data) })
      if (!response.ok) throw new Error(String(response.status))
      form.reset()
      setState('sent')
      // A Guestbook on the page shows the new message at once.
      window.dispatchEvent(new CustomEvent('site-form-sent', { detail: name }))
    } catch {
      setState('failed')
    }
  }

  const control = 'w-full rounded-md border border-border bg-background px-3 py-2 text-foreground outline-none focus-visible:ring-2 focus-visible:ring-primary'

  return (
    <form onSubmit={submit} className={className ?? 'grid gap-4'}>
      {fields.map((field) => {
        const id = `${name}-${field.name}`
        return (
          <div key={field.name} className='grid gap-1.5'>
            <label htmlFor={id} className='text-sm font-medium text-foreground'>
              {field.label}
              {field.required ? <span className='text-muted'> ({text.required})</span> : null}
            </label>
            {field.type === 'textarea' ? (
              <textarea id={id} name={field.name} required={field.required} placeholder={field.placeholder} rows={5} className={control} />
            ) : field.type === 'select' ? (
              <select id={id} name={field.name} required={field.required} className={control} defaultValue=''>
                <option value='' disabled>
                  {field.placeholder ?? '...'}
                </option>
                {(field.options ?? []).map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            ) : (
              <input id={id} name={field.name} type={field.type ?? 'text'} required={field.required} placeholder={field.placeholder} className={control} />
            )}
          </div>
        )
      })}
      <input type='text' name='_hp' tabIndex={-1} autoComplete='off' aria-hidden='true' className='hidden' />
      <button type='submit' disabled={state === 'sending'} className='rounded-md bg-primary px-5 py-2.5 font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:opacity-60'>
        {state === 'sending' ? text.sending : (submitLabel ?? text.send)}
      </button>
      <p role='status' aria-live='polite' className={state === 'failed' ? 'text-sm text-foreground' : 'text-sm text-muted'}>
        {state === 'sent' ? (successMessage ?? text.sent) : state === 'failed' ? text.failed : ''}
      </p>
    </form>
  )
}

// Also the default export, so both ways of importing it work.
export default SiteForm
