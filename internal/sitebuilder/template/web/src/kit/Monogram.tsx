type MonogramProps = {
  /** The letters to show, e.g. "R & N". */
  letters: string
  /** circle, arch or square frame. */
  frame?: 'circle' | 'arch' | 'square'
  className?: string
}

/** Initials in a frame, for invitations and personal sites. */
export function Monogram({ letters, frame = 'circle', className }: MonogramProps) {
  const shape = frame === 'arch' ? 'rounded-t-full rounded-b-md' : frame === 'square' ? 'rounded-md' : 'rounded-full'
  return (
    <div className={className ?? `mx-auto flex size-28 items-center justify-center border border-accent text-accent ${shape}`}>
      <span className='font-heading text-3xl tracking-wide'>{letters}</span>
    </div>
  )
}

export default Monogram
