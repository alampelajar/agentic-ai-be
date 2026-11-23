import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import type { Item, Tone } from './types'
import { Section, SectionHeading, cx, frame, mutedClass } from './ui'

type FeaturesProps = {
  /** The heading of the section, e.g. "Layanan kami" or "Menu". */
  title: string
  /** One sentence under the heading. */
  intro?: string
  /** The services, products, menu items or reasons. Two to nine work best. */
  items: Item[]
  tone?: Tone
  id?: string
}

/** A list of services, products, menu items or reasons. The look decides whether they are rows, cards, a grid or columns. */
export function Features({ title, intro, items, tone = 'auto', id }: FeaturesProps) {
  const list = (items ?? []).filter((item) => item && item.title)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const muted = mutedClass(tone)
  const name = cx('font-heading text-xl leading-snug md:text-2xl', look.caps && 'uppercase')
  const icon = (item: Item, circle = true) =>
    item.icon ? (
      <span aria-hidden='true' className={cx('inline-flex shrink-0 items-center justify-center text-primary', circle && 'size-12 rounded-full bg-accent/15')}>
        {item.icon}
      </span>
    ) : null

  let body
  switch (look.list) {
    case 'cards':
      body = (
        <Stagger className={cx('grid gap-6 sm:grid-cols-2', list.length % 3 === 0 || list.length > 4 ? 'lg:grid-cols-3' : '')}>
          {list.map((item, index) => (
            <article key={item.title} className={cx('lift flex h-full flex-col gap-4 rounded-xl bg-surface p-7 text-foreground', frame(), look.tilt && (index % 2 ? 'md:rotate-1' : 'md:-rotate-1'))}>
              {icon(item)}
              <h3 className={name}>{item.title}</h3>
              {item.text ? <p className='leading-relaxed text-muted'>{item.text}</p> : null}
              {item.note ? <p className='mt-auto pt-2 font-semibold'>{item.note}</p> : null}
            </article>
          ))}
        </Stagger>
      )
      break
    case 'boxes':
      body = (
        <Stagger className='grid border-l-2 border-t-2 border-border sm:grid-cols-2 lg:grid-cols-3'>
          {list.map((item) => (
            <article key={item.title} className='flex h-full flex-col gap-4 border-b-2 border-r-2 border-border p-8 transition-colors hover:bg-surface'>
              {icon(item, false)}
              <h3 className={cx(name, 'font-black')}>{item.title}</h3>
              {item.text ? <p className={cx('leading-relaxed', muted)}>{item.text}</p> : null}
              {item.note ? <p className='mt-auto font-semibold'>{item.note}</p> : null}
            </article>
          ))}
        </Stagger>
      )
      break
    case 'bento':
      body = (
        <Stagger className='grid gap-5 md:grid-cols-3' itemClassName='md:first:col-span-2 md:first:row-span-2'>
          {list.map((item, index) => (
            <article key={item.title} className={cx('lift flex h-full flex-col gap-4 rounded-xl bg-surface p-7 text-foreground', frame(), index === 0 && 'md:p-10')}>
              {icon(item)}
              <h3 className={cx(name, index === 0 && 'md:text-4xl')}>{item.title}</h3>
              {item.text ? <p className={cx('leading-relaxed text-muted', index === 0 && 'md:text-lg')}>{item.text}</p> : null}
              {item.note ? <p className='mt-auto font-semibold'>{item.note}</p> : null}
            </article>
          ))}
        </Stagger>
      )
      break
    case 'columns':
      body = (
        <Stagger className='grid gap-x-16 gap-y-10 md:grid-cols-2'>
          {list.map((item) => (
            <article key={item.title} className='flex gap-5'>
              <span aria-hidden='true' className='mt-3 size-2.5 shrink-0 bg-accent' />
              <div>
                <h3 className={name}>
                  {item.title}
                  {item.note ? <span className={cx('ml-3 text-base font-normal', muted)}>{item.note}</span> : null}
                </h3>
                {item.text ? <p className={cx('mt-2 leading-relaxed', muted)}>{item.text}</p> : null}
              </div>
            </article>
          ))}
        </Stagger>
      )
      break
    case 'tiles':
      body = (
        <Stagger className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
          {list.map((item) => (
            <article key={item.title} className={cx('lift relative flex h-full flex-col gap-3 bg-surface p-6 text-foreground', frame(), look.button === 'pill' ? 'rounded-2xl' : 'rounded-md')}>
              {item.note ? <span className='absolute -top-3 right-4 rotate-2 bg-accent px-3 py-1 text-xs font-bold text-on-accent'>{item.note}</span> : null}
              {icon(item, false)}
              <h3 className={cx(name, 'text-lg md:text-xl')}>{item.title}</h3>
              {item.text ? <p className='text-sm leading-relaxed text-muted'>{item.text}</p> : null}
            </article>
          ))}
        </Stagger>
      )
      break
    default:
      // rows: one line each, name on the left and the note on the right, separated by thin lines.
      body = (
        <Stagger as='ul' itemAs='li' className='border-t border-border' itemClassName='border-b border-border'>
          {list.map((item) => (
            <div key={item.title} className='grid gap-2 py-6 md:grid-cols-12 md:gap-8 md:py-8'>
              <h3 className={cx(name, 'md:col-span-4')}>{item.title}</h3>
              <p className={cx('leading-relaxed md:col-span-6', muted)}>{item.text}</p>
              {item.note ? <p className='font-semibold md:col-span-2 md:text-right'>{item.note}</p> : null}
            </div>
          ))}
        </Stagger>
      )
  }

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      {body}
    </Section>
  )
}

/** A safe id from a heading. */
export function slug(text: string): string {
  return (
    text
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '')
      .slice(0, 40) || 'section'
  )
}

export default Features
