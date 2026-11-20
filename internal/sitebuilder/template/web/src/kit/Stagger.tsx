import { Children, type ElementType, type ReactNode } from 'react'
import { Reveal } from './Reveal'

type StaggerProps = {
  children?: ReactNode
  /** Where each child comes from, as in Reveal. */
  from?: 'up' | 'down' | 'left' | 'right' | 'fade' | 'zoom'
  /** The element around the children, e.g. 'ul'. Default 'div'. */
  as?: ElementType
  /** The element around each child, e.g. 'li'. Default 'div'. */
  itemAs?: ElementType
  /** Classes of the group: put the grid or flex classes here. */
  className?: string
  /** Classes of each child's wrapper. */
  itemClassName?: string
}

/** A group whose children appear one after another when it is scrolled into view. */
export function Stagger({ children, from = 'up', as: Tag = 'div', itemAs = 'div', className, itemClassName }: StaggerProps) {
  return (
    <Tag className={className}>
      {Children.toArray(children).map((child, index) => (
        <Reveal key={index} from={from} order={index} as={itemAs} className={itemClassName}>
          {child}
        </Reveal>
      ))}
    </Tag>
  )
}

export default Stagger
