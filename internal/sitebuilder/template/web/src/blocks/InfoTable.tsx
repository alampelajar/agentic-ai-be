import { look } from '../look'
import { Reveal } from '../kit/Reveal'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame } from './ui'

type InfoTableProps = {
  /** The heading, e.g. "Jam buka dan pengiriman". */
  title: string
  intro?: string
  /** Label and value pairs from the brief: days and hours, delivery area, ways to pay. */
  rows: { label: string; value: string }[]
  tone?: Tone
  id?: string
}

/** Practical information as a short table: hours, delivery, payment, what to bring. */
export function InfoTable({ title, intro, rows, tone = 'auto', id }: InfoTableProps) {
  const list = (rows ?? []).filter((row) => row && row.label && row.value)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Reveal from='up' className={cx('overflow-hidden rounded-xl bg-background text-foreground', look.centered && 'mx-auto max-w-3xl', frame() || 'border border-border')}>
        <table className='w-full text-left'>
          <tbody>
            {list.map((row) => (
              <tr key={row.label} className='border-b border-border last:border-b-0'>
                <th scope='row' className={cx('w-2/5 px-6 py-5 align-top font-semibold', look.caps && 'uppercase tracking-wide')}>
                  {row.label}
                </th>
                <td className='px-6 py-5 text-muted'>{row.value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Reveal>
    </Section>
  )
}

export default InfoTable
