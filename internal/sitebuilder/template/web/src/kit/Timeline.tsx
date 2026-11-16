export type TimelineItem = { time: string; title: string; place?: string; note?: string }

type TimelineProps = {
  items: TimelineItem[]
  className?: string
}

/** The plan of a day or an event, as times on a line. */
export function Timeline({ items, className }: TimelineProps) {
  return (
    <ol className={className ?? 'grid gap-8 border-l border-border pl-6'}>
      {items.map((item) => (
        <li key={`${item.time}-${item.title}`} className='relative'>
          <span aria-hidden='true' className='absolute -left-8 top-1.5 size-3 rounded-full border border-primary bg-background' />
          <p className='text-sm uppercase tracking-widest text-accent'>{item.time}</p>
          <h3 className='mt-1 text-xl text-foreground'>{item.title}</h3>
          {item.place ? <p className='mt-1 text-muted'>{item.place}</p> : null}
          {item.note ? <p className='mt-2 text-foreground'>{item.note}</p> : null}
        </li>
      ))}
    </ol>
  )
}

export default Timeline
