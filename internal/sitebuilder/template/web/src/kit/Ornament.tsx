type OrnamentProps = {
  /** line: a thin rule; diamond: a rule with a small diamond; flourish: a curl in the middle; dots: three dots. */
  kind?: 'line' | 'diamond' | 'flourish' | 'dots'
  className?: string
}

/** A decorative divider between sections, drawn in the theme's accent colour. */
export function Ornament({ kind = 'diamond', className }: OrnamentProps) {
  const box = className ?? 'mx-auto my-10 w-full max-w-xs text-accent'
  if (kind === 'line') return <hr aria-hidden='true' className={`${box} border-0 border-t border-current`} />
  if (kind === 'dots')
    return (
      <div aria-hidden='true' className={`${box} flex justify-center gap-3`}>
        <span className='size-1.5 rounded-full bg-current' />
        <span className='size-1.5 rounded-full bg-current' />
        <span className='size-1.5 rounded-full bg-current' />
      </div>
    )
  return (
    <svg aria-hidden='true' viewBox='0 0 240 24' className={box} fill='none' stroke='currentColor' strokeWidth='1'>
      {kind === 'flourish' ? (
        <>
          <path d='M0 12 H84 C96 12 98 4 108 6 C114 7 114 14 120 12 C126 14 126 7 132 6 C142 4 144 12 156 12 H240' />
          <circle cx='120' cy='12' r='2' fill='currentColor' />
        </>
      ) : (
        <>
          <path d='M0 12 H104 M136 12 H240' />
          <path d='M120 4 L128 12 L120 20 L112 12 Z' />
        </>
      )}
    </svg>
  )
}

export default Ornament
