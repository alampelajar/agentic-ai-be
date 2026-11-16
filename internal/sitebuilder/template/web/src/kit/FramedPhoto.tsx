type FramedPhotoProps = {
  src: string
  alt: string
  /** thin: a fine border with space; arch: rounded top; offset: a second frame behind. */
  frame?: 'thin' | 'arch' | 'offset'
  className?: string
}

/** One of the owner's photos in a frame. Renders nothing without a picture. */
export function FramedPhoto({ src, alt, frame = 'thin', className }: FramedPhotoProps) {
  if (!src) return null
  if (frame === 'offset')
    return (
      <div className={className ?? 'relative mx-auto w-full max-w-md'}>
        <div aria-hidden='true' className='absolute inset-0 translate-x-3 translate-y-3 rounded-md border border-accent' />
        <img src={src} alt={alt} className='relative aspect-4/5 w-full rounded-md object-cover' />
      </div>
    )
  const shape = frame === 'arch' ? 'rounded-t-full rounded-b-md' : 'rounded-md'
  return (
    <div className={className ?? `mx-auto w-full max-w-md border border-border bg-surface p-3 ${shape}`}>
      <img src={src} alt={alt} className={`aspect-4/5 w-full object-cover ${shape}`} />
    </div>
  )
}

export default FramedPhoto
