import { Check } from 'lucide-react'
import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Action, Tone } from './types'
import { Actions, Section, SectionHeading, cx, frame, mutedClass } from './ui'

type Plan = {
  /** The name of the package, e.g. "Paket Hemat". */
  name: string
  /** The price exactly as the brief gives it, e.g. "Rp150.000". Never an invented price. */
  price: string
  /** What the price is for, e.g. "per orang" or "per bulan". */
  per?: string
  /** One sentence on who it suits. */
  text?: string
  /** What is included, short phrases from the brief. */
  includes?: string[]
  /** Set apart as the one most people choose (at most one, only when the brief says so). */
  highlight?: boolean
  /** A short label on the highlighted one, e.g. "Paling dipilih". */
  badge?: string
  /** The button of this package, e.g. a WhatsApp message to order it. */
  action?: Action
}

type PricingProps = {
  /** The heading, e.g. "Paket dan harga". */
  title: string
  intro?: string
  /** Two to four packages with the prices the brief gives. */
  plans: Plan[]
  /** A short line under the packages, e.g. what is not included. */
  note?: string
  tone?: Tone
  id?: string
}

/** Packages with their prices and what each includes, side by side; one may be set apart. Shows only prices the brief gives. */
export function Pricing({ title, intro, plans, note, tone = 'auto', id }: PricingProps) {
  const list = (plans ?? []).filter((plan) => plan && plan.name && plan.price).slice(0, 4)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const shape = look.button === 'pill' ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'
  const columns = list.length === 1 ? 'mx-auto max-w-md' : list.length === 2 ? 'mx-auto max-w-4xl md:grid-cols-2' : list.length === 3 ? 'md:grid-cols-3' : 'sm:grid-cols-2 lg:grid-cols-4'

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Stagger className={cx('grid items-stretch gap-6', columns)}>
        {list.map((plan) => {
          const strong = Boolean(plan.highlight)
          const cardTone: Tone = strong ? 'primary' : 'plain'
          return (
            <article
              key={plan.name}
              className={cx(
                'lift relative flex h-full flex-col gap-6 p-8',
                shape,
                strong ? 'bg-primary text-primary-foreground md:-my-3 md:py-11' : cx('bg-surface text-foreground', frame() || 'border border-border'),
              )}
            >
              {strong && plan.badge ? (
                <span className={cx('absolute -top-3 left-8 bg-accent px-3 py-1 text-xs font-bold text-on-accent', look.button === 'square' ? '' : 'rounded-full', look.caps && 'uppercase tracking-wide')}>{plan.badge}</span>
              ) : null}
              <div>
                <h3 className={cx('font-heading text-2xl', look.caps && 'uppercase tracking-wide')}>{plan.name}</h3>
                {plan.text ? <p className={cx('mt-2 leading-relaxed', mutedClass(cardTone))}>{plan.text}</p> : null}
              </div>
              <p className='flex flex-wrap items-baseline gap-x-2'>
                <span className='font-heading text-4xl tracking-tight md:text-5xl'>{plan.price}</span>
                {plan.per ? <span className={mutedClass(cardTone)}>{plan.per}</span> : null}
              </p>
              {plan.includes?.length ? (
                <ul className='space-y-3 border-t border-current/15 pt-6'>
                  {plan.includes.filter(Boolean).map((line) => (
                    <li key={line} className='flex items-start gap-3'>
                      <Check className={cx('mt-0.5 size-5 shrink-0', strong ? '' : 'text-accent')} aria-hidden='true' />
                      <span>{line}</span>
                    </li>
                  ))}
                </ul>
              ) : null}
              {plan.action ? <Actions actions={[{ ...plan.action, kind: strong ? 'primary' : 'secondary' }]} tone={cardTone} className='mt-auto pt-2' /> : null}
            </article>
          )
        })}
      </Stagger>
      {note ? <p className={cx('mt-10 text-center text-sm', mutedClass(tone))}>{note}</p> : null}
    </Section>
  )
}

export default Pricing
