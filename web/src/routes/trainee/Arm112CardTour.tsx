import type { RefObject } from "react";
import { SpotlightTour, type TourStep } from "./Arm112Tour";

const steps: readonly TourStep[] = [
  [
    { target: "card-aon", text: "АОН — номер, с которого поступил вызов. Он появляется автоматически." },
    { target: "card-provided-phone", text: "Запишите телефон, который назвал заявитель. Кнопка «АОН» скопирует входящий номер." },
    { target: "card-on-site-phone", text: "Укажите телефон на месте происшествия, если он известен." },
  ],
  [
    { target: "card-applicant-name", text: "Запишите фамилию и имя заявителя со слов звонящего." },
    { target: "card-applicant-status", text: "Выберите, кем заявитель приходится происшествию." },
    { target: "card-channel", text: "Выберите канал связи или оператора связи, если он известен." },
  ],
  [
    { target: "card-address-summary", text: "Здесь собирается краткий адрес из заполненных полей. Проверьте его перед сохранением." },
    { target: "card-address-country", text: "Укажите страну, где произошло событие." },
    { target: "card-address-region", text: "Укажите субъект: область, край или республику." },
  ],
  [
    { target: "card-address-city", text: "Укажите населённый пункт." },
    { target: "card-address-object", text: "Если событие произошло на именованном объекте, укажите его название." },
    { target: "card-address-okrug", text: "Укажите административный округ, если он известен." },
  ],
  [
    { target: "card-address-district", text: "Укажите район, если он известен." },
    { target: "card-address-street", text: "Укажите улицу происшествия." },
    { target: "card-address-house", text: "Укажите номер дома или владения." },
  ],
  [
    { target: "card-address-building", text: "Добавьте корпус, если он есть." },
    { target: "card-address-structure", text: "Укажите строение или сооружение, если оно есть." },
    { target: "card-address-flat", text: "Укажите квартиру или офис, если это поможет найти место." },
  ],
  [
    { target: "card-address-entrance", text: "Укажите подъезд, если он известен." },
    { target: "card-address-floor", text: "Укажите этаж, если он известен." },
    { target: "card-address-code", text: "Укажите код домофона, если заявитель его сообщил." },
  ],
  [
    { target: "card-address-landmark", text: "Добавьте ориентир, который поможет найти место." },
    { target: "card-address-descriptive", text: "Если точного адреса нет, запишите описание места со слов заявителя." },
    { target: "card-address-clear", text: "Эта кнопка очищает все поля адреса." },
  ],
];

export function Arm112CardTour({ rootRef, onClose }: { rootRef: RefObject<HTMLElement>; onClose: () => void }) {
  return <SpotlightTour rootRef={rootRef} onClose={onClose} steps={steps} closePosition="left" label="Заполнение карточки 112" />;
}
