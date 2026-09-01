# tonyTheHelper

A Thai-language household stock tracker, used inside LINE. A family records what they
keep at home, how much of it is left, and who changed that.

Every term below carries its canonical Thai UI label. The English term is what appears in
code; the Thai term is what appears on screen. Neither may drift from the other.

## Language

**User**:
A person, identified by their LINE account. Exists independently of any household.
_Thai_: ผู้ใช้
_Avoid_: account, profile, member (a member is a user *in* a household)

**Household**:
A group of people who share one stock of items. The unit of ownership for everything
in the app: items belong to a household, never to a user.
_Thai_: บ้าน
_Avoid_: family, group, home, team

**Member**:
A user who belongs to a household. Any member may create items and change quantities.
_Thai_: สมาชิก
_Avoid_: participant, user (when household-scoped)

**Owner**:
The member who created the household. The only member who may add or remove members.
Ownership is about people, not about items.
_Thai_: เจ้าของบ้าน
_Avoid_: admin, head, creator

**Item**:
A named thing a household keeps in stock, with a current quantity and a unit.
An item is a *kind* of thing ("น้ำปลา"), not a single physical object.
_Thai_: ของ
_Avoid_: product, SKU, goods, stock, thing

**Icon**:
A symbol chosen from a fixed list to stand in for an item's photo. A fallback, not an
alternative: an item with a photo shows the photo. The point is that giving an item a
picture should not require a camera.
_Thai_: ไอคอน
_Avoid_: emoji (an implementation detail), avatar, image, thumbnail

**Unit**:
What is being counted for an item.
_Thai_: หน่วยนับ
_Avoid_: unit type, uom, measure, packaging

**Quantity**:
How many units of an item the household currently has. Authoritative and mutable.
_Thai_: จำนวน
_Avoid_: amount, count, stock level, balance

**Stock Movement**:
An append-only record of a single change to an item's quantity: who changed it, when,
from what to what, and an optional note. Never edited, never deleted.
_Thai_: ประวัติการเปลี่ยนแปลง
_Avoid_: transaction, adjustment, log entry, history, audit

**Invite**:
An unguessable, expiring token that grants one person membership of one household. Created
by the owner, delivered as a LINE message, and consumed by opening it. Tapping an invite
you have already used is not an error — it simply opens the household.
_Thai_: คำเชิญ
_Avoid_: invitation code, join code, token, link

**Low-stock threshold**:
An optional quantity per item. When an item's quantity falls to or below it, the item is
*low*. One of the two things that can make an item low, not the only one — see *Marked
low*. An item with no threshold is never low on the quantity's account alone.
_Thai_: จำนวนขั้นต่ำ
_Avoid_: reorder point, minimum, par level, alert level

**Marked low**:
A member's statement that an item is running out, made by hand and regardless of any
threshold. It exists for what a threshold cannot express: a household keeps one bottle of
shampoo, so the quantity reads 1 the day it is bought and 1 the day it is empty. Cleared
by restocking the item, not by shopping for it.
_Thai_: ทำเครื่องหมายว่าใกล้หมด
_Avoid_: flagged, starred, needed, pinned, wanted

**Low**:
An item that either is at or below its low-stock threshold, or has been marked low. Either
source alone is enough. Low items sort to the top of the stock and make up the shopping
list.
_Thai_: ใกล้หมด
_Avoid_: running out, short, depleted, below par

**Shopping list**:
The low items, read as one list to take to the shop. A view over the stock rather than a
thing anyone keeps: an item joins it by becoming low and leaves it by being restocked,
never by being ticked off. There is no separate list to edit, and nothing to reconcile
when two members shop at once.
_Thai_: รายการที่ต้องซื้อ
_Avoid_: basket, cart, to-buy list, wishlist, needs
