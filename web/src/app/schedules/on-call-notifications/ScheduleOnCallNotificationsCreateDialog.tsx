import React, { useState } from 'react'
import FormDialog from '../../dialogs/FormDialog'
import ScheduleOnCallNotificationsForm from './ScheduleOnCallNotificationsForm'
import { useOnCallRulesData, useSetOnCallRulesSubmit } from './hooks'
import { NO_DAY, Value, channelFieldsFromType, mapOnCallErrors } from './util'

interface ScheduleOnCallNotificationsCreateDialogProps {
  onClose: () => void
  scheduleID: string
}

const defaultValue: Value = {
  time: null,
  weekdayFilter: NO_DAY,
  type: 'SLACK',
  channelFields: {
    slackChannelID: null,
  },
}

export default function ScheduleOnCallNotificationsCreateDialog(
  props: ScheduleOnCallNotificationsCreateDialogProps,
): JSX.Element {
  const { onClose, scheduleID } = props
  const [value, setValue] = useState<Value>(defaultValue)

  const { q, zone, rules } = useOnCallRulesData(scheduleID)

  const { m, submit } = useSetOnCallRulesSubmit(
    scheduleID,
    zone,
    value,
    ...rules,
  )

  const [dialogErrors, fieldErrors] = mapOnCallErrors(m.error, q.error)
  const busy = (q.loading && !zone) || m.loading

  const setNewValue = (newValue: Value): void => {
    console.log(newValue)
    let channelFields = newValue.channelFields
    if (value.type !== newValue.type) {
      console.log('resetting channel fields')
      channelFields = channelFieldsFromType(newValue.type)
    }
    console.log({ ...newValue, channelFields })
    setValue({ ...newValue, channelFields })
  }

  return (
    <FormDialog
      title='Create Notification Rule'
      errors={dialogErrors}
      loading={busy}
      onClose={onClose}
      onSubmit={() => submit().then(onClose)}
      form={
        <ScheduleOnCallNotificationsForm
          scheduleID={scheduleID}
          errors={fieldErrors}
          value={value}
          onChange={setNewValue}
        />
      }
    />
  )
}
