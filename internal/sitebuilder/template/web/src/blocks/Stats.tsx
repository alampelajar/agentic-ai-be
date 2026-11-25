import { look } from '../look'
import { CountUp } from '../kit/CountUp'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, mutedClass } from './ui'

type Stat = {
  /** A number the brief states, written directly (value: 12). Never an invented number (checked). */
  value: number
  prefix?: string
  /** E.g. "+" or " tahun". */
  suffix?: string
  /** What the number counts, e.g. "tahun berdiri". */
  label: string
}

type StatsProps = {
  /** A heading, only when the numbers need one. */
  title?: string
  intro?: string
  /** Two to four numbers from the brief. */
  items: Stat[]
  tone?: Tone
  id?: string
}

/** A row of numbers the owner gave (years open, branches, dishes on the menu) that count up when they come into view. */
export function Stats({ title, intro, items, tone = 'auto', id }: StatsProps) {
  const list = (items ?? []).filter((item) => item && typeof item.value === 'number' && item.label).slice(0, 4)
  if (list.length === 0) return null
  const headingId = title ? `${id ?? slug(title)}-title` : undefined
  const columns = list.length === 2 ? 'sm:grid-cols-2' : list.length === 3 ? 'sm:grid-cols-3' : 'grid-cols-2 lg:grid-cols-4'
  const divided = look.frame !== 'none'

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      {title ? <SectionHeading id={headingId} title={title} intro={intro} tone={tone} /> : null}
      <Stagger as='dl' className={cx('grid gap-y-10', columns, divided && 'sm:divide-x sm:divide-current/15')}>
        {list.map((item) => (
          <div key={item.label} className={cx('flex flex-col gap-2 px-4', look.centered || !title ? 'items-center text-center' : '')}>
            <dt className={cx('order-2 text-base md:text-lg', mutedClass(tone))}>{item.label}</dt>
            <dd className={cx('order-1 font-heading text-5xl leading-none tracking-tight md:text-6xl', look.caps && 'uppercase')}>
              <CountUp to={item.value} prefix={item.prefix} suffix={item.suffix} />
            </dd>
          </div>
        ))}
      </Stagger>
    </Section>
  )
}

export default Stats
