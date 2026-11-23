import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, mutedClass } from './ui'

type StepsProps = {
  /** The heading, e.g. "Cara memesan". */
  title: string
  intro?: string
  /** Two to five steps, in order. */
  items: { title: string; text?: string }[]
  tone?: Tone
  id?: string
}

/** Steps in order: how to order, how it works, how it is made. */
export function Steps({ title, intro, items, tone = 'auto', id }: StepsProps) {
  const list = (items ?? []).filter((item) => item && item.title).slice(0, 6)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const muted = mutedClass(tone)
  const onColour = tone === 'primary' || tone === 'dark'
  const dot = cx(
    'relative z-10 inline-flex size-12 shrink-0 items-center justify-center rounded-full font-heading text-lg font-bold',
    onColour ? 'bg-background text-foreground' : 'bg-primary text-primary-foreground',
  )

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      {look.steps === 'timeline' ? (
        <Stagger as='ol' itemAs='li' className='relative mx-auto max-w-3xl space-y-10 before:absolute before:bottom-6 before:left-6 before:top-6 before:w-px before:bg-current/20'>
          {list.map((step, index) => (
            <div key={step.title} className='flex gap-6'>
              <span className={dot}>{index + 1}</span>
              <div className='pt-2'>
                <h3 className={cx('font-heading text-xl md:text-2xl', look.caps && 'uppercase')}>{step.title}</h3>
                {step.text ? <p className={cx('mt-2 leading-relaxed', muted)}>{step.text}</p> : null}
              </div>
            </div>
          ))}
        </Stagger>
      ) : (
        <Stagger
          as='ol'
          itemAs='li'
          className={cx('relative grid gap-10', list.length === 4 ? 'md:grid-cols-4' : list.length === 2 ? 'md:grid-cols-2' : 'md:grid-cols-3', 'md:before:absolute md:before:left-6 md:before:right-6 md:before:top-6 md:before:h-px md:before:bg-current/20')}
        >
          {list.map((step, index) => (
            <div key={step.title} className='flex gap-5 md:flex-col'>
              <span className={dot}>{index + 1}</span>
              <div>
                <h3 className={cx('font-heading text-xl md:text-2xl', look.caps && 'font-black uppercase')}>{step.title}</h3>
                {step.text ? <p className={cx('mt-2 leading-relaxed', muted)}>{step.text}</p> : null}
              </div>
            </div>
          ))}
        </Stagger>
      )}
    </Section>
  )
}

export default Steps
