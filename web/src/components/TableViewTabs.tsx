import React from "react";

export function TableViewTabs<T extends string>({
  value,
  items,
  onChange,
}: {
  value: T;
  items: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div className="table-view-tabs" role="tablist">
      {items.map((item) => (
        <button
          key={item.value}
          className={value === item.value ? "active" : ""}
          role="tab"
          aria-selected={value === item.value}
          onClick={() => onChange(item.value)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
