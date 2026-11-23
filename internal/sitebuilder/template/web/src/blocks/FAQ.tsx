import { ChevronDown } from 'lucide-react'
import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame } from './ui'

type FAQProps = {
  /** The heading, e.g. "Pertanyaan yang sering ditanyakan". */
  title: string
  intro?: string
  /** Three to seven questions a visitor really asks, answered only with facts from the brief. */
  items: { question: string; answer: string }[]
  tone?: Tone
  id?: string
}

/** Common questions that open one at a time. Works with the keyboard and without script. */
export function FAQ({ title, intro, items, tone = 'auto', id }: FAQProps) {
  const list = (items ?? []).filter((item) => item && item.question && item.answer)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Stagger className={cx('space-y-4', look.centered ? 'mx-auto max-w-3xl' : 'max-w-4xl')}>
        {list.map((item) => (
          <details key={item.question} className={cx('group rounded-xl bg-background px-6 text-foreground', frame() || 'border border-border')}>
            <summary className='flex cursor-pointer list-none items-center justify-between gap-6 py-5 text-lg font-semibold [&::-webkit-details-marker]:hidden'>
              {item.question}
              <ChevronDown className='size-5 shrink-0 transition-transform group-open:rotate-180' aria-hidden='true' />
            </summary>
            <p className='pb-6 leading-relaxed text-muted'>{item.answer}</p>
          </details>
        ))}
      </Stagger>
    </Section>
  )
}

export default FAQ
