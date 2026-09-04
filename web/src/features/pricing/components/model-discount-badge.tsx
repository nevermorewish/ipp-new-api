import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import { getModelPriceDiscount } from '../lib/price'
import type { PricingModel } from '../types'

export function ModelDiscountBadge({ model }: { model: PricingModel }) {
  const { t } = useTranslation()
  const discount = getModelPriceDiscount(model)
  if (!discount) return null
  return (
    <Badge variant='secondary' className='shrink-0 text-[10px]'>
      {t('{{discount}}/10 of original price', { discount })}
    </Badge>
  )
}
