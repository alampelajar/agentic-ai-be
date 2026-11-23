import { site } from '../site'
import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx } from './ui'

type GalleryProps = {
  title: string
  intro?: string
  /** At most this many photos. */
  limit?: number
  tone?: Tone
  id?: string
}

/** The owner's photos under a heading. Shows nothing at all, not even the heading, when there are no photos. */
export function Gallery({ title, intro, limit = 9, tone = 'auto', id }: GalleryProps) {
  const photos = site.images.slice(0, limit)
  if (photos.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const rounded = look.button === 'pill' ? 'rounded-2xl' : look.button === 'soft' ? 'rounded-lg' : ''
  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Stagger from='zoom' className='grid grid-cols-2 gap-3 md:grid-cols-3 md:gap-5' itemClassName='md:first:col-span-2 md:first:row-span-2'>
        {photos.map((photo) => (
          <img key={photo.src} src={photo.src} alt={photo.alt} loading='lazy' className={cx('aspect-square h-full w-full object-cover', rounded)} />
        ))}
      </Stagger>
    </Section>
  )
}

export default Gallery
