import React from "react";
import { Domain } from "../types/api";

interface DomainPillProps {
  domains: Domain[];
  onSelection: (domain: Domain) => void;
  selected: Domain | null;
}

export const DomainPill: React.FC<DomainPillProps> = ({
  domains,
  onSelection,
  selected,
}) => {
  return (
    <div className="domain-pill-container">
      <h2 className="domain-pill-title">Select an area to get started:</h2>
      <div className="pill-list">
        {domains.map((domain) => (
          <button
            key={domain.key}
            className={selected?.key === domain.key ? "pill selected" : "pill"}
            onClick={() => onSelection(domain)}
          >
            <span className="pill-icon" aria-hidden="true">{domain.icon}</span>
            <span className="pill-text">{domain.display_name}</span>
          </button>
        ))}
      </div>
    </div>
  );
};
