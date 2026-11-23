import type { ReactNode } from 'react'
import { look } from '../look'
import { Link } from '../router'
import { whatsappLink } from '../site'
import { Reveal } from '../kit/Reveal'
import type { Action, Tone } from './types'

// Shared by every section: the content width, the room around a section, the edge of
// a card, the heading and the buttons, all shaped by the look.

export const cx = (...parts: (string | false | null | undefined)[]) => parts.filter(Boolean).join(' ')

/** The content width and the equal side margins of every section. */
export const container = 'mx-auto w-full max-w-6xl px-5 sm:px-8'

/** The edge of a card or panel, from the look. */
export function frame(): string {
  switch (look.frame) {
    case 'bold':
      return 'border-2 border-border'
    case 'dashed':
      return 'border border-dashed border-border'
    case 'double':
      return 'border-4 border-double border-border'
    case 'none':
      return ''
    default:
      return 'border border-border'
  }
}

/** Colours of a section's background and text. 'auto' alternates with its neighbours (theme.css). */
export function toneClass(tone: Tone): string {
  switch (tone) {
    case 'surface':
      return 'bg-surface text-foreground'
    case 'primary':
      return 'bg-primary text-primary-foreground'
    case 'dark':
      return 'bg-foreground text-background'
    default:
      return 'bg-background text-foreground'
  }
}

/** Muted text that stays readable on every tone. */
export function mutedClass(tone: Tone): string {
  return tone === 'primary' || tone === 'dark' ? 'opacity-80' : 'text-muted'
}

type SectionProps = {
  children?: ReactNode
  tone?: Tone
  id?: string
  className?: string
  /** The heading's id, so the section is named by its heading. */
  labelledBy?: string
}

/** One band of the page, with the room the look asks for above and below. */
export function Section({ children, tone = 'auto', id, className, labelledBy }: SectionProps) {
  return (
    <section id={id} aria-labelledby={labelledBy} data-band={tone === 'auto' ? '' : undefined} className={cx(toneClass(tone), look.airy ? 'py-24 md:py-32' : 'py-20 md:py-28', className)}>
      <div className={container}>{children}</div>
    </section>
  )
}

type HeadingProps = {
  id?: string
  title: string
  intro?: string
  tone?: Tone
  /** Centre this heading even when the look does not. */
  center?: boolean
  className?: string
}

/** The heading of a section and one short sentence under it. */
export function SectionHeading({ id, title, intro, tone = 'auto', center, className }: HeadingProps) {
  const centred = center ?? look.centered
  return (
    <Reveal from='up' className={cx('mb-12 max-w-3xl md:mb-16', centred && 'mx-auto text-center', className)}>
      {look.rule ? <span aria-hidden='true' className={cx('mb-6 block h-1.5 w-14 bg-accent', centred && 'mx-auto')} /> : null}
      <h2 id={id} className={cx('font-heading text-3xl leading-tight tracking-tight md:text-5xl', look.caps && 'uppercase')}>
        {title}
      </h2>
      {intro ? <p className={cx('mt-5 text-lg leading-relaxed md:text-xl', mutedClass(tone))}>{intro}</p> : null}
    </Reveal>
  )
}

/** The classes of a button of the given kind, shaped by the look. */
export function buttonClass(kind: 'primary' | 'secondary', tone: Tone = 'plain'): string {
  if (look.button === 'link') {
    return cx(
      'inline-flex items-center gap-2 py-2 text-base font-semibold underline decoration-2 underline-offset-8 transition-colors hover:decoration-accent',
      kind === 'primary' ? 'decoration-accent' : 'decoration-current opacity-80',
    )
  }
  const shape = look.button === 'pill' ? 'rounded-full' : look.button === 'soft' ? 'rounded-lg' : 'rounded-none'
  const edge = look.frame === 'bold' ? 'border-2' : 'border'
  const onColour = tone === 'primary' || tone === 'dark'
  const fill =
    kind === 'primary'
      ? onColour
        ? cx(edge, 'border-transparent bg-background text-foreground hover:bg-surface')
        : cx(edge, look.frame === 'bold' ? 'border-border' : 'border-transparent', 'bg-primary text-primary-foreground hover:opacity-90')
      : onColour
        ? cx(edge, 'border-current bg-transparent hover:bg-background/10')
        : cx(edge, 'border-border bg-transparent text-foreground hover:bg-surface')
  return cx('lift inline-flex min-h-12 items-center justify-center gap-2 px-6 py-3 text-base font-semibold transition-colors', shape, fill, look.caps && 'uppercase tracking-wide')
}

/** Where an action goes, or '' when it cannot go anywhere (no WhatsApp number). */
export function actionHref(action: Action): string {
  if (action.whatsapp !== undefined) return whatsappLink(action.whatsapp)
  return action.to ?? action.href ?? ''
}

/** A row of buttons. Actions that go nowhere are left out. */
export function Actions({ actions, tone = 'auto', className }: { actions?: Action[]; tone?: Tone; className?: string }) {
  const usable = (actions ?? []).filter((action) => action.label && actionHref(action))
  if (usable.length === 0) return null
  return (
    <div className={cx('flex flex-wrap items-center gap-3', className)}>
      {usable.map((action, index) => (
        <Link key={action.label} to={actionHref(action)} className={buttonClass(action.kind ?? (index === 0 ? 'primary' : 'secondary'), tone)}>
          {action.label}
        </Link>
      ))}
    </div>
  )
}

/** The first of the owner's photos, or null. */
export { firstPhoto } from './photos'
