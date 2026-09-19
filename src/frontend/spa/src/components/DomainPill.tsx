import React from "react";
import { Domain } from "../types";

interface DomainPillProps {
  domain: Domain;
  onSelect: (domain: Domain) => void;
}

export const DomainPill: React.FC<DomainPillProps> = ({ domain, onSelect }) => {
  return (
    <button
      className="domain-pill"
      onClick={() => onSelect(domain)}
    >
      <span className="domain-pill-icon">{domain.icon}</span>
      <span className="domain-pill-name">{domain.display_name}</span>
    </button>
  );
};

interface DomainPillsProps {
  domains: Domain[];
  onSelection: (domain: Domain) => void;
}

export const DomainPills: React.FC<DomainPillsProps> = ({ domains, onSelection }) => {
  return (
    <div className="domain-pills">
      {domains.map((domain) => (
        <DomainPill key={domain.key} domain={domain} onSelect={onSelection} />
      ))}
    </div>
  );
};
