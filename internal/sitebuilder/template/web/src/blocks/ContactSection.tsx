import { Clock, Mail, MapPin, MessageCircle, Phone } from 'lucide-react'
import type { ReactNode } from 'react'
import { look } from '../look'
import { site, whatsappLink } from '../site'
import { SiteForm, type SiteFormField } from '../components/SiteForm'
import { MapLink } from '../kit/MapLink'
import { Reveal } from '../kit/Reveal'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, buttonClass, cx, frame } from './ui'

type ContactSectionProps = {
  /** The heading, e.g. "Hubungi kami" or "Pesan sekarang". */
  title: string
  intro?: string
  /** A form beside the details. Its answers are stored on the site; only the owner reads them. */
  form?: { name: string; fields: FieldInput[]; submitLabel?: string; successMessage?: string }
  /** The words of the WhatsApp button. */
  whatsappLabel?: string
  /** The message the WhatsApp button prepares. */
  whatsappMessage?: string
  tone?: Tone
  id?: string
}

/** A form field as a page may write it; turned into the form's own field shape. */
export type FieldInput = {
  name: string
  label: string
  type?: string
  required?: boolean
  placeholder?: string
  options?: (string | { label?: string; value?: string })[]
}

const FIELD_TYPES: SiteFormField['type'][] = ['text', 'email', 'tel', 'textarea', 'select', 'date', 'number']

export function toField(input: FieldInput): SiteFormField {
  const type = FIELD_TYPES.includes(input.type as SiteFormField['type']) ? (input.type as SiteFormField['type']) : 'text'
  const options = input.options?.map((option) => (typeof option === 'string' ? option : (option.label ?? option.value ?? ''))).filter(Boolean)
  return { name: input.name, label: input.label, type, required: input.required, placeholder: input.placeholder, options }
}

const words = {
  id: { whatsapp: 'Chat lewat WhatsApp', phone: 'Telepon', email: 'E-mail', address: 'Alamat', hours: 'Jam buka' },
  en: { whatsapp: 'Chat on WhatsApp', phone: 'Phone', email: 'E-mail', address: 'Address', hours: 'Opening hours' },
}

/** How to get in touch: the WhatsApp button, phone, e-mail, address with a map link and the hours, all from the site's details, beside an optional form. Empty details are left out. */
export function ContactSection({ title, intro, form, whatsappLabel, whatsappMessage, tone = 'auto', id }: ContactSectionProps) {
  const t = words[site.language] ?? words.id
  const c = site.contact
  const wa = whatsappLink(whatsappMessage ?? '')
  const headingId = `${id ?? slug(title)}-title`
  const rows: { icon: ReactNode; label: string; content: ReactNode }[] = []
  if (c.phone) rows.push({ icon: <Phone className='size-5' />, label: t.phone, content: <a href={`tel:${c.phone.replace(/[^0-9+]/g, '')}`} className='hover:underline'>{c.phone}</a> })
  if (c.email) rows.push({ icon: <Mail className='size-5' />, label: t.email, content: <a href={`mailto:${c.email}`} className='break-all hover:underline'>{c.email}</a> })
  if (c.address)
    rows.push({
      icon: <MapPin className='size-5' />,
      label: t.address,
      content: (
        <span className='flex flex-col gap-1'>
          {c.address}
          <MapLink address={c.address} className='text-sm font-semibold underline underline-offset-4' />
        </span>
      ),
    })
  if (c.hours) rows.push({ icon: <Clock className='size-5' />, label: t.hours, content: c.hours })

  const details = (
    <div className='flex flex-col gap-8'>
      {wa ? (
        <a href={wa} target='_blank' rel='noreferrer' className={cx(buttonClass('primary', tone), 'w-fit')}>
          <MessageCircle className='size-5' aria-hidden='true' />
          {whatsappLabel ?? t.whatsapp}
        </a>
      ) : null}
      {rows.length ? (
        <dl className='space-y-6'>
          {rows.map((row) => (
            <div key={row.label} className='flex gap-4'>
              <dt className='mt-0.5 shrink-0 text-accent'>
                <span aria-hidden='true'>{row.icon}</span>
                <span className='sr-only'>{row.label}</span>
              </dt>
              <dd className='text-lg'>{row.content}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </div>
  )

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      {form && form.fields?.length ? (
        <div className='grid gap-12 md:grid-cols-12'>
          <Reveal from='up' className={cx('rounded-xl bg-background p-6 text-foreground sm:p-8 md:col-span-7', frame() || 'border border-border')}>
            <SiteForm name={form.name} fields={form.fields.map(toField)} submitLabel={form.submitLabel} successMessage={form.successMessage} />
          </Reveal>
          <Reveal from='up' delay={150} className='md:col-span-5'>
            {details}
          </Reveal>
        </div>
      ) : (
        <Reveal from='up' className={look.centered ? 'mx-auto max-w-xl' : ''}>
          {details}
        </Reveal>
      )}
    </Section>
  )
}

export default ContactSection
