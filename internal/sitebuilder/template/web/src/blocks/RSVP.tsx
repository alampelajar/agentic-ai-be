import { look } from '../look'
import { SiteForm } from '../components/SiteForm'
import { Guestbook } from '../kit/Guestbook'
import { Reveal } from '../kit/Reveal'
import { toField, type FieldInput } from './ContactSection'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame } from './ui'

type RSVPProps = {
  /** The heading, e.g. "Konfirmasi kehadiran". */
  title: string
  intro?: string
  /** The form of the brief. For a guestbook form it has fields named exactly "name" and "message"; attendance and the number of guests stay private. */
  form: { name: string; fields: FieldInput[]; submitLabel?: string; successMessage?: string }
  /** True only when the form is marked guestbook in the brief: the wishes are then shown beside the form. */
  guestbook?: boolean
  /** The heading over the wishes, e.g. "Ucapan dan doa". */
  messagesTitle?: string
  tone?: Tone
  id?: string
}

/** Replying to an invitation: the form, and for a guest book the wishes beside it, newest first. */
export function RSVP({ title, intro, form, guestbook = false, messagesTitle, tone = 'auto', id }: RSVPProps) {
  if (!form || !form.name || !form.fields?.length) return null
  const headingId = `${id ?? slug(title)}-title`
  const shape = look.button === 'pill' ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'
  const card = cx('bg-background p-6 text-foreground sm:p-8', shape, frame() || 'border border-border')
  const formCard = (
    <div className={card}>
      <SiteForm name={form.name} fields={form.fields.map(toField)} submitLabel={form.submitLabel} successMessage={form.successMessage} />
    </div>
  )

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} center={!guestbook || undefined} />
      {guestbook ? (
        <div className='grid gap-10 md:grid-cols-2 md:gap-12'>
          <Reveal from='up'>{formCard}</Reveal>
          <Reveal from='up' delay={150} className='flex flex-col gap-5'>
            {messagesTitle ? <h3 className={cx('font-heading text-2xl', look.caps && 'uppercase')}>{messagesTitle}</h3> : null}
            <div className='max-h-128 overflow-y-auto pr-1'>
              <Guestbook form={form.name} />
            </div>
          </Reveal>
        </div>
      ) : (
        <Reveal from='up' className='mx-auto max-w-xl'>
          {formCard}
        </Reveal>
      )}
    </Section>
  )
}

export default RSVP
