# Логическая модель данных

![[ER.svg]]

<details>
@startuml

entity Promise {
  * ID: uuid
  --
  * text: text
  * due: datetime
  BeneficiaryID: uuid
  ParentPromiseID: uuid
}

entity Beneficiary {
  * ID: uuid
  --
  * name: string
}

entity Commitment {
  * ID: uuid
  --
  PromiseID: int
  CommiterID: int
  due: datetime
}

entity Commiter {
  * ID: int
  --
  * name: string
}

Commitment }|--|| Promise : belongs to
Commitment }|--|| Commiter : made by
Promise }o--|| Beneficiary : has
Promise }--o Promise : parent of

@enduml
</details>