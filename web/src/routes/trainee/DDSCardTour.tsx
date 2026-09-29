import { useMemo, type RefObject } from "react";
import { SpotlightTour, type TourStep, type TourTipPlacement } from "./Arm112Tour";

const baseSteps: readonly TourStep[] = [
  [
    { target: "dds-aon", text: "АОН — номер, с которого поступил вызов в 112." },
    { target: "dds-provided-phone", text: "Телефон, который сообщил заявитель. Он может отличаться от АОН." },
    { target: "dds-on-site-phone", text: "Контактный телефон непосредственно на месте происшествия, если он известен." },
  ],
  [
    { target: "dds-card-identity", text: "Здесь находятся номер, время регистрации и текущий статус карточки." },
    { target: "dds-timer", text: "Таймер показывает срок для текущего этапа обработки; после просрочки он становится красным." },
    { target: "dds-applicant", text: "Имя заявителя и его роль указаны в карточке, полученной от 112." },
  ],
  [
    { target: "dds-flags", text: "Проверьте сведения о пострадавших и других важных обстоятельствах." },
    { target: "dds-address", text: "Адрес места происшествия помогает определить, куда направлять бригаду." },
    { target: "dds-incident-type", text: "Здесь указан тип происшествия, выбранный при регистрации карточки." },
  ],
  [
    { target: "dds-description", text: "Прочитайте описание со слов заявителя перед принятием решения." },
    { target: "dds-comms-header", text: "В этом разделе отображается связь с бригадой и другими контактами сценария." },
    { target: "dds-phone-header", text: "Это учебный телефонный симулятор ДДС для исходящих звонков." },
  ],
  [
    { target: "dds-phone-contacts", text: "Выберите адресата из контактов, назначенных сценарием. Номер в шапке карточки сам по себе сюда не добавляется." },
    { target: "dds-phone-call", text: "Кнопка начинает учебный исходящий звонок выбранному контакту." },
    { target: "dds-comms-log", text: "Здесь сохраняются уведомления, входящие и исходящие звонки." },
  ],
];

const servicesStep: TourStep = [
  { target: "dds-services", text: "Внизу перечислены службы, которым направлена карточка, и их статусы." },
];
const backStep = { target: "dds-back", text: "Вернитесь к списку происшествий, не завершая обработку карточки." };

const placements: Readonly<Record<number, TourTipPlacement>> = {
  3: "beside-left",
  4: "beside-left",
};

export function DDSCardTour({ rootRef, onClose, hasPhone, hasOwnService }: {
  rootRef: RefObject<HTMLElement>;
  onClose: () => void;
  hasPhone: boolean;
  hasOwnService: boolean;
}) {
  const steps = useMemo(() => {
    const result = hasPhone ? [...baseSteps] : baseSteps.slice(0, 3);
    if (hasOwnService) return [...result, servicesStep, [
      { target: "dds-own-service", text: "Это ваша служба. Карандаш открывает изменение статуса, а стрелка — историю статусов." },
      backStep,
    ]];
    return [...result, servicesStep, [backStep]];
  }, [hasPhone, hasOwnService]);

  return <SpotlightTour rootRef={rootRef} onClose={onClose} steps={steps}
    tipPlacements={placements} closePosition="left" label="Карточка ДДС" />;
}
